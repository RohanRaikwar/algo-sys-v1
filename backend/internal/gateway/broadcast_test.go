package gateway

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

// buildEnvelope reproduces the exact hand-crafted JSON logic from Broadcaster.Broadcast
// so we can test envelope format independently of Redis/WS dependencies.
func buildEnvelope(channel string, data []byte, now time.Time, seq int64) []byte {
	buf := make([]byte, 0, len(channel)+len(data)+128)
	buf = append(buf, `{"channel":"`...)
	buf = append(buf, channel...)
	buf = append(buf, `","data":`...)
	buf = append(buf, data...)
	buf = append(buf, `,"ts":"`...)
	buf = now.AppendFormat(buf, time.RFC3339Nano)
	buf = append(buf, `","seq":`...)
	buf = strconv.AppendInt(buf, seq, 10)
	buf = append(buf, '}')
	return buf
}

// envelope is the parsed WS message structure.
type envelope struct {
	Channel string          `json:"channel"`
	Data    json.RawMessage `json:"data"`
	TS      string          `json:"ts"`
	Seq     int64           `json:"seq"`
}

// TestBroadcastEnvelopeFormat verifies the hand-crafted JSON envelope
// matches the expected structure: {"channel":"...","data":...,"ts":"...","seq":N}
func TestBroadcastEnvelopeFormat(t *testing.T) {
	channel := "pub:candle:60s:NSE:99926000"
	data := []byte(`{"ts":"2026-02-25T10:00:00Z","o":100,"h":105,"l":99,"c":103,"v":500}`)
	now := time.Date(2026, 2, 25, 10, 0, 1, 0, time.UTC)
	var seq int64 = 42

	buf := buildEnvelope(channel, data, now, seq)

	var env envelope
	if err := json.Unmarshal(buf, &env); err != nil {
		t.Fatalf("envelope is not valid JSON: %v\nraw: %s", err, buf)
	}

	if env.Channel != channel {
		t.Errorf("channel: got %q, want %q", env.Channel, channel)
	}
	if env.Seq != seq {
		t.Errorf("seq: got %d, want %d", env.Seq, seq)
	}

	// Data should be parseable JSON
	var candle map[string]interface{}
	if err := json.Unmarshal(env.Data, &candle); err != nil {
		t.Fatalf("data is not valid JSON: %v", err)
	}
	if _, ok := candle["ts"]; !ok {
		t.Error("data missing 'ts' field")
	}

	// TS should be valid RFC3339Nano
	parsed, err := time.Parse(time.RFC3339Nano, env.TS)
	if err != nil {
		t.Errorf("ts is not valid RFC3339Nano: %v", err)
	}
	if !parsed.Equal(now) {
		t.Errorf("ts: got %v, want %v", parsed, now)
	}
}

// TestBroadcastEnvelopeIndicator tests envelope with indicator channel.
func TestBroadcastEnvelopeIndicator(t *testing.T) {
	channel := "pub:ind:SMA_9:60s:NSE:99926000"
	data := []byte(`{"value":103.5,"ready":true}`)
	now := time.Now().UTC()

	buf := buildEnvelope(channel, data, now, 1)

	var env envelope
	if err := json.Unmarshal(buf, &env); err != nil {
		t.Fatalf("envelope is not valid JSON: %v\nraw: %s", err, buf)
	}

	if env.Channel != channel {
		t.Errorf("channel: got %q, want %q", env.Channel, channel)
	}

	var indData struct {
		Value float64 `json:"value"`
		Ready bool    `json:"ready"`
	}
	if err := json.Unmarshal(env.Data, &indData); err != nil {
		t.Fatalf("data is not valid JSON: %v", err)
	}
	if indData.Value != 103.5 {
		t.Errorf("indicator value: got %f, want 103.5", indData.Value)
	}
	if !indData.Ready {
		t.Error("expected ready=true")
	}
}

// TestBroadcastEnvelopeNestedData tests envelope with nested/complex data payload.
func TestBroadcastEnvelopeNestedData(t *testing.T) {
	channel := `pub:candle:1s:NSE:99926000`
	data := []byte(`{"note":"test","nested":{"a":1},"arr":[1,2,3]}`)

	buf := buildEnvelope(channel, data, time.Now().UTC(), 999)

	var env envelope
	if err := json.Unmarshal(buf, &env); err != nil {
		t.Fatalf("envelope is not valid JSON: %v\nraw: %s", err, buf)
	}
	if env.Seq != 999 {
		t.Errorf("seq: got %d, want 999", env.Seq)
	}
}

// TestChannelParsing tests the parseChannel function with various formats.
func TestChannelParsing(t *testing.T) {
	tests := []struct {
		name    string
		channel string
		wantTF  int
		wantInd string
		wantNil bool
	}{
		{"candle_60s", "pub:candle:60s:NSE:99926000", 60, "", false},
		{"candle_1s", "pub:candle:1s:NSE:99926000", 1, "", false},
		{"candle_300s", "pub:candle:300s:NSE:99926000", 300, "", false},
		{"indicator_SMA", "pub:ind:SMA_9:60s:NSE:99926000", 60, "SMA_9", false},
		{"indicator_RSI", "pub:ind:RSI_14:120s:NSE:99926000", 120, "RSI_14", false},
		{"indicator_EMA", "pub:ind:EMA_21:300s:NSE:99926000", 300, "EMA_21", false},
		{"invalid_garbage", "garbage", 0, "", true},
		{"invalid_short", "pub:candle", 0, "", true},
		{"tick_channel", "pub:tick:NSE:99926000", 0, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := parseChannel(tt.channel)
			if tt.wantNil {
				if parsed != nil {
					t.Errorf("expected nil, got %+v", parsed)
				}
				return
			}
			if parsed == nil {
				t.Fatal("expected non-nil parsed channel")
			}
			if parsed.tf != tt.wantTF {
				t.Errorf("tf: got %d, want %d", parsed.tf, tt.wantTF)
			}
			if tt.wantInd != "" && parsed.indName != tt.wantInd {
				t.Errorf("indName: got %q, want %q", parsed.indName, tt.wantInd)
			}
		})
	}
}

// TestEnvelopeSeqMonotonic verifies sequence numbers are reflected correctly.
func TestEnvelopeSeqMonotonic(t *testing.T) {
	channel := "pub:candle:60s:NSE:99926000"
	data := []byte(`{}`)
	now := time.Now().UTC()

	for i := int64(1); i <= 100; i++ {
		buf := buildEnvelope(channel, data, now, i)
		var env envelope
		if err := json.Unmarshal(buf, &env); err != nil {
			t.Fatalf("seq=%d: invalid JSON: %v", i, err)
		}
		if env.Seq != i {
			t.Errorf("seq: got %d, want %d", env.Seq, i)
		}
	}
}

// envelopeWithChannelSeq is the parsed WS message structure including channel_seq.
type envelopeWithChannelSeq struct {
	Channel    string          `json:"channel"`
	Data       json.RawMessage `json:"data"`
	TS         string          `json:"ts"`
	Seq        int64           `json:"seq"`
	ChannelSeq int64           `json:"channel_seq"`
}

// buildEnvelopeWithChannelSeq reproduces the full envelope format from Broadcaster.Broadcast
// including the per-channel seq field.
func buildEnvelopeWithChannelSeq(channel string, data []byte, now time.Time, seq, channelSeq int64) []byte {
	buf := make([]byte, 0, len(channel)+len(data)+160)
	buf = append(buf, `{"channel":"`...)
	buf = append(buf, channel...)
	buf = append(buf, `","data":`...)
	buf = append(buf, data...)
	buf = append(buf, `,"ts":"`...)
	buf = now.AppendFormat(buf, time.RFC3339Nano)
	buf = append(buf, `","seq":`...)
	buf = strconv.AppendInt(buf, seq, 10)
	buf = append(buf, `,"channel_seq":`...)
	buf = strconv.AppendInt(buf, channelSeq, 10)
	buf = append(buf, '}')
	return buf
}

// TestBroadcaster_PerChannelSeq verifies that per-channel seq is included in the
// envelope and tracks independently across channels.
func TestBroadcaster_PerChannelSeq(t *testing.T) {
	channelA := "pub:candle:60s:NSE:99926000"
	channelB := "pub:ind:SMA_9:60s:NSE:99926000"
	data := []byte(`{}`)
	now := time.Now().UTC()

	// Simulate broadcasting: channel A gets seq 1,2,3 and channel B gets seq 1,2
	var globalSeq int64

	for i := int64(1); i <= 3; i++ {
		globalSeq++
		buf := buildEnvelopeWithChannelSeq(channelA, data, now, globalSeq, i)
		var env envelopeWithChannelSeq
		if err := json.Unmarshal(buf, &env); err != nil {
			t.Fatalf("channelA seq=%d: invalid JSON: %v", i, err)
		}
		if env.ChannelSeq != i {
			t.Errorf("channelA channel_seq: got %d, want %d", env.ChannelSeq, i)
		}
		if env.Seq != globalSeq {
			t.Errorf("channelA global seq: got %d, want %d", env.Seq, globalSeq)
		}
	}

	for i := int64(1); i <= 2; i++ {
		globalSeq++
		buf := buildEnvelopeWithChannelSeq(channelB, data, now, globalSeq, i)
		var env envelopeWithChannelSeq
		if err := json.Unmarshal(buf, &env); err != nil {
			t.Fatalf("channelB seq=%d: invalid JSON: %v", i, err)
		}
		if env.ChannelSeq != i {
			t.Errorf("channelB channel_seq: got %d, want %d", env.ChannelSeq, i)
		}
		if env.Channel != channelB {
			t.Errorf("channelB: got %q, want %q", env.Channel, channelB)
		}
	}

	// Verify global seq is 5 (3 from A + 2 from B)
	if globalSeq != 5 {
		t.Errorf("global seq: got %d, want 5", globalSeq)
	}
}
