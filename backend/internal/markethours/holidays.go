package markethours

import "time"

// NSE holidays for 2026.
// Source: NSE India official holiday list.
// Format: month, day pairs.
var nseHolidays2026 = []struct {
	month time.Month
	day   int
}{
	{time.January, 26},  // Republic Day
	{time.February, 17}, // Mahashivratri (tentative)
	{time.March, 14},    // Holi
	{time.March, 31},    // Id-ul-Fitr (Eid) (tentative)
	{time.April, 2},     // Ram Navami (tentative)
	{time.April, 6},     // Mahavir Jayanti
	{time.April, 10},    // Good Friday
	{time.April, 14},    // Dr. Ambedkar Jayanti
	{time.May, 1},       // Maharashtra Day
	{time.June, 7},      // Bakrid / Eid ul-Adha (tentative)
	{time.July, 6},      // Muharram (tentative)
	{time.August, 15},   // Independence Day
	{time.August, 16},   // Janmashtami (tentative)
	{time.September, 5}, // Milad-un-Nabi (tentative)
	{time.October, 2},   // Mahatma Gandhi Jayanti
	{time.October, 20},  // Dussehra
	{time.October, 21},  // Dussehra (tentative)
	{time.November, 5},  // Diwali / Lakshmi Puja (tentative)
	{time.November, 6},  // Diwali Balipratipada (tentative)
	{time.November, 7},  // Bhai Dooj (tentative)
	{time.November, 19}, // Guru Nanak Jayanti
	{time.December, 25}, // Christmas
}

// pre-compute for fast lookup
var holidaySet map[string]bool

func init() {
	holidaySet = make(map[string]bool, len(nseHolidays2026))
	for _, h := range nseHolidays2026 {
		key := dateKey(2026, h.month, h.day)
		holidaySet[key] = true
	}
}

// IsHoliday returns true if the date (in IST) is an NSE holiday.
func IsHoliday(t time.Time) bool {
	ist := t.In(IST)
	return holidaySet[dateKey(ist.Year(), ist.Month(), ist.Day())]
}

func dateKey(year int, month time.Month, day int) string {
	return time.Date(year, month, day, 0, 0, 0, 0, IST).Format("2006-01-02")
}
