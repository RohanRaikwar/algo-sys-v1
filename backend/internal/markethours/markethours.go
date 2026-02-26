package markethours

import (
	"fmt"
	"time"
)

// IST is the Indian Standard Time location (UTC+5:30).
var IST = time.FixedZone("IST", 5*3600+30*60)

// Market hours in IST
const (
	OpenHour    = 9
	OpenMinute  = 15
	CloseHour   = 15
	CloseMinute = 30

	// Pre-market warm-up timing (ADR-006)
	PreOpenMinutesBefore   = 5 // wake 5 min before open → 9:10 AM for login
	WSConnectMinutesBefore = 1 // connect WS 1 min before open → 9:14 AM
)

// IsMarketOpen returns true if t falls within NSE trading hours
// (9:15 AM – 3:30 PM IST, Mon–Fri, excluding holidays).
func IsMarketOpen(t time.Time) bool {
	ist := t.In(IST)
	wd := ist.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return false
	}
	if IsHoliday(ist) {
		return false
	}
	hm := ist.Hour()*60 + ist.Minute()
	return hm >= OpenHour*60+OpenMinute && hm < CloseHour*60+CloseMinute
}

// IsWeekday returns true if t is Mon–Fri.
func IsWeekday(t time.Time) bool {
	wd := t.In(IST).Weekday()
	return wd >= time.Monday && wd <= time.Friday
}

// IsTradingDay returns true if t is a weekday and not a holiday.
func IsTradingDay(t time.Time) bool {
	ist := t.In(IST)
	return IsWeekday(ist) && !IsHoliday(ist)
}

// NextOpen returns the next market open time (9:15 AM IST on next trading day).
// If t is before today's open on a trading day, returns today's open.
func NextOpen(t time.Time) time.Time {
	ist := t.In(IST)

	// Try today first
	todayOpen := time.Date(ist.Year(), ist.Month(), ist.Day(), OpenHour, OpenMinute, 0, 0, IST)
	if ist.Before(todayOpen) && IsTradingDay(ist) {
		return todayOpen
	}

	// Otherwise find the next trading day
	d := ist.AddDate(0, 0, 1)
	for i := 0; i < 10; i++ { // max 10 days ahead (holidays + weekends)
		if IsTradingDay(d) {
			return time.Date(d.Year(), d.Month(), d.Day(), OpenHour, OpenMinute, 0, 0, IST)
		}
		d = d.AddDate(0, 0, 1)
	}
	// Fallback: next day
	return time.Date(ist.Year(), ist.Month(), ist.Day()+1, OpenHour, OpenMinute, 0, 0, IST)
}

// NextPreOpen returns the next pre-market warm-up time (9:10 AM on next trading day).
// This is PreOpenMinutesBefore minutes before market open, used to start login/token generation.
func NextPreOpen(t time.Time) time.Time {
	open := NextOpen(t)
	return open.Add(-time.Duration(PreOpenMinutesBefore) * time.Minute)
}

// WSConnectTime returns the WS connect time for the given open time.
// This is WSConnectMinutesBefore minutes before market open (9:14 AM).
func WSConnectTime(openTime time.Time) time.Time {
	return openTime.Add(-time.Duration(WSConnectMinutesBefore) * time.Minute)
}

// TodayClose returns today's market close time (3:30 PM IST).
func TodayClose(t time.Time) time.Time {
	ist := t.In(IST)
	return time.Date(ist.Year(), ist.Month(), ist.Day(), CloseHour, CloseMinute, 0, 0, IST)
}

// TimeUntilClose returns the duration until today's close.
// Returns 0 if market is already closed.
func TimeUntilClose(t time.Time) time.Duration {
	cl := TodayClose(t)
	d := cl.Sub(t.In(IST))
	if d < 0 {
		return 0
	}
	return d
}

// TimeUntilOpen returns the duration until the next market open.
func TimeUntilOpen(t time.Time) time.Duration {
	return NextOpen(t).Sub(t.In(IST))
}

// StatusString returns a human-readable market status.
func StatusString(t time.Time) string {
	if IsMarketOpen(t) {
		d := TimeUntilClose(t)
		return fmt.Sprintf("Market Open — closes in %s", fmtDur(d))
	}
	next := NextOpen(t)
	d := next.Sub(t)
	ist := next.In(IST)
	return fmt.Sprintf("Market Closed — opens %s %s (%s)",
		ist.Weekday().String()[:3], ist.Format("15:04"), fmtDur(d))
}

func fmtDur(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
