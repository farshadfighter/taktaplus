package reports

import (
	"fmt"
	"strings"
	"time"
)

// Conversions between the Gregorian and the Solar Hijri (Jalali) calendars,
// after the algorithm of jdf.scr.ir, valid for the years this product
// will see.

func toJalali(gy, gm, gd int) (int, int, int) {
	gdm := [...]int{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}
	gy2 := gy
	if gm > 2 {
		gy2 = gy + 1
	}
	days := 355666 + 365*gy + (gy2+3)/4 - (gy2+99)/100 + (gy2+399)/400 + gd + gdm[gm-1]
	jy := -1595 + 33*(days/12053)
	days %= 12053
	jy += 4 * (days / 1461)
	days %= 1461
	if days > 365 {
		jy += (days - 1) / 365
		days = (days - 1) % 365
	}
	var jm, jd int
	if days < 186 {
		jm = 1 + days/31
		jd = 1 + days%31
	} else {
		jm = 7 + (days-186)/30
		jd = 1 + (days-186)%30
	}
	return jy, jm, jd
}

func toGregorian(jy, jm, jd int) (int, int, int) {
	jy += 1595
	days := -355668 + 365*jy + (jy/33)*8 + ((jy%33)+3)/4 + jd
	if jm < 7 {
		days += (jm - 1) * 31
	} else {
		days += (jm-7)*30 + 186
	}
	gy := 400 * (days / 146097)
	days %= 146097
	if days > 36524 {
		days--
		gy += 100 * (days / 36524)
		days %= 36524
		if days >= 365 {
			days++
		}
	}
	gy += 4 * (days / 1461)
	days %= 1461
	if days > 365 {
		gy += (days - 1) / 365
		days = (days - 1) % 365
	}
	gd := days + 1
	leap := (gy%4 == 0 && gy%100 != 0) || gy%400 == 0
	months := []int{0, 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if leap {
		months[2] = 29
	}
	gm := 1
	for gm <= 12 && gd > months[gm] {
		gd -= months[gm]
		gm++
	}
	return gy, gm, gd
}

// jalaliMonthStart returns local midnight on day 1 of the Jalali month that
// contains t, shifted by add months.
func jalaliMonthStart(t time.Time, add int) time.Time {
	jy, jm, _ := toJalali(t.Year(), int(t.Month()), t.Day())
	jm += add
	for jm < 1 {
		jm += 12
		jy--
	}
	for jm > 12 {
		jm -= 12
		jy++
	}
	gy, gm, gd := toGregorian(jy, jm, 1)
	return time.Date(gy, time.Month(gm), gd, 0, 0, 0, 0, t.Location())
}

// jalaliDate returns the local midnight of a Jalali date.
func jalaliDate(jy, jm, jd int, loc *time.Location) time.Time {
	gy, gm, gd := toGregorian(jy, jm, jd)
	return time.Date(gy, time.Month(gm), gd, 0, 0, 0, 0, loc)
}

var faDigits = strings.NewReplacer("0", "۰", "1", "۱", "2", "۲", "3", "۳", "4", "۴", "5", "۵", "6", "۶", "7", "۷", "8", "۸", "9", "۹")

// FaDate formats t as a Jalali date with Persian digits, e.g. ۱۴۰۵/۰۸/۰۶.
func FaDate(t time.Time) string {
	jy, jm, jd := toJalali(t.Year(), int(t.Month()), t.Day())
	return faDigits.Replace(fmt.Sprintf("%04d/%02d/%02d", jy, jm, jd))
}

// FaDateTime adds the time of day.
func FaDateTime(t time.Time) string {
	return FaDate(t) + " " + faDigits.Replace(t.Format("15:04"))
}

// jalaliStamp is a Jalali date with Latin digits for file names.
func jalaliStamp(t time.Time) string {
	jy, jm, jd := toJalali(t.Year(), int(t.Month()), t.Day())
	return fmt.Sprintf("%04d%02d%02d", jy, jm, jd)
}
