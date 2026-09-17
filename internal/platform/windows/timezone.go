package windows

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"time"
)

// SystemTimeRule 描述 Windows 的第 N 个星期几转换规则。
type SystemTimeRule struct {
	Month     uint16
	DayOfWeek uint16
	Day       uint16
	Hour      uint16
	Minute    uint16
	Second    uint16
}

// TimeZoneRule 保存生成 IANA TZif 所需的 Windows 偏移和夏令时规则。
type TimeZoneRule struct {
	Name         string
	Bias         int32
	StandardBias int32
	DaylightBias int32
	StandardDate SystemTimeRule
	DaylightDate SystemTimeRule
}

// BuildLocationFromRules 将按年份提供的 Windows 规则生成可处理未来 DST 的 time.Location。
func BuildLocationFromRules(name string, rules map[int]TimeZoneRule, firstYear, lastYear int) (*time.Location, error) {
	if firstYear > lastYear {
		return nil, errors.New("build time zone: year range is invalid")
	}
	if len(rules) == 0 {
		return nil, errors.New("build time zone: rules are empty")
	}
	standard, ok := rules[firstYear]
	if !ok {
		standard = rules[nearestRuleYear(rules, firstYear)]
	}
	standardOffset := offsetSeconds(standard.Bias, standard.StandardBias)
	daylightOffset := offsetSeconds(standard.Bias, standard.DaylightBias)
	baseStandardOffset := standardOffset
	baseDaylightOffset := daylightOffset
	hasDaylight := false
	transitions := make([]tzTransition, 0, (lastYear-firstYear+1)*2)
	for year := firstYear; year <= lastYear; year++ {
		rule, found := rules[year]
		if !found {
			rule = rules[nearestRuleYear(rules, year)]
		}
		standardOffset = offsetSeconds(rule.Bias, rule.StandardBias)
		daylightOffset = offsetSeconds(rule.Bias, rule.DaylightBias)
		if rule.DaylightDate.Month == 0 || rule.StandardDate.Month == 0 || standardOffset == daylightOffset {
			continue
		}
		hasDaylight = true
		start, err := transitionTime(year, rule.DaylightDate, standardOffset)
		if err != nil {
			return nil, fmt.Errorf("build time zone: daylight transition %d: %w", year, err)
		}
		end, err := transitionTime(year, rule.StandardDate, daylightOffset)
		if err != nil {
			return nil, fmt.Errorf("build time zone: standard transition %d: %w", year, err)
		}
		transitions = append(transitions, tzTransition{at: start.Unix(), typeIndex: 1, year: year})
		transitions = append(transitions, tzTransition{at: end.Unix(), typeIndex: 0, year: year})
	}
	sort.SliceStable(transitions, func(i, j int) bool { return transitions[i].at < transitions[j].at })
	filtered := transitions[:0]
	for _, transition := range transitions {
		if transition.at >= time.Date(firstYear, 1, 1, 0, 0, 0, 0, time.UTC).Unix() && transition.at < time.Date(lastYear+1, 1, 1, 0, 0, 0, 0, time.UTC).Unix() {
			filtered = append(filtered, transition)
		}
	}
	if !hasDaylight {
		return fixedLocation(name, standardOffset), nil
	}
	return time.LoadLocationFromTZData(name, buildTZif(baseStandardOffset, baseDaylightOffset, filtered, standard.Name))
}

// tzTransition 表示 TZif 中的一次 UTC 转换。
type tzTransition struct {
	at        int64
	typeIndex byte
	year      int
}

// offsetSeconds 将 Windows Bias 分钟转换为 POSIX UTC 偏移秒数。
func offsetSeconds(bias, adjustment int32) int32 {
	return -(bias + adjustment) * 60
}

// transitionTime 计算第 N 个星期几的本地转换时刻并转换到 UTC。
func transitionTime(year int, rule SystemTimeRule, beforeOffset int32) (time.Time, error) {
	if rule.Month < 1 || rule.Month > 12 || rule.Day < 1 || rule.Day > 5 || rule.DayOfWeek > 6 || rule.Hour > 23 || rule.Minute > 59 || rule.Second > 59 {
		return time.Time{}, errors.New("system time rule is invalid")
	}
	first := time.Date(year, time.Month(rule.Month), 1, int(rule.Hour), int(rule.Minute), int(rule.Second), 0, time.FixedZone("", int(beforeOffset)))
	dayDelta := (int(rule.DayOfWeek) - int(first.Weekday()) + 7) % 7
	day := 1 + dayDelta + (int(rule.Day)-1)*7
	if rule.Day == 5 {
		last := time.Date(year, time.Month(rule.Month)+1, 0, int(rule.Hour), int(rule.Minute), int(rule.Second), 0, first.Location())
		day = last.Day() - (int(last.Weekday())-int(rule.DayOfWeek)+7)%7
	}
	if time.Date(year, time.Month(rule.Month), day, int(rule.Hour), int(rule.Minute), int(rule.Second), 0, first.Location()).Month() != time.Month(rule.Month) {
		return time.Time{}, errors.New("system time rule exceeds month")
	}
	return time.Date(year, time.Month(rule.Month), day, int(rule.Hour), int(rule.Minute), int(rule.Second), 0, first.Location()).UTC(), nil
}

// nearestRuleYear 选择距离目标年份最近的可用规则。
func nearestRuleYear(rules map[int]TimeZoneRule, year int) int {
	selected, distance := 0, int(^uint(0)>>1)
	for candidate := range rules {
		if current := abs(candidate - year); current < distance {
			selected, distance = candidate, current
		}
	}
	return selected
}

// abs 返回整数绝对值。
func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// fixedLocation 创建无夏令时转换的固定时区。
func fixedLocation(name string, offset int32) *time.Location {
	if name == "" {
		name = "Windows"
	}
	return time.FixedZone(name, int(offset))
}

// buildTZif 序列化有限年份范围的 TZif v2 数据，使 time 包执行完整 DST 转换。
func buildTZif(standardOffset, daylightOffset int32, transitions []tzTransition, name string) []byte {
	if name == "" {
		name = "STD"
	}
	abbreviations := []byte("STD\x00DST\x00")
	writeHeader := func(buffer *bytes.Buffer, version byte, transitionCount int) {
		buffer.WriteString("TZif")
		buffer.WriteByte(version)
		buffer.Write(make([]byte, 15))
		for _, value := range []int32{0, 0, 0, int32(transitionCount), 2, int32(len(abbreviations))} {
			_ = binary.Write(buffer, binary.BigEndian, value)
		}
	}
	writeData := func(buffer *bytes.Buffer, wide bool, values []tzTransition) {
		for _, transition := range values {
			if wide {
				_ = binary.Write(buffer, binary.BigEndian, transition.at)
			} else {
				_ = binary.Write(buffer, binary.BigEndian, int32(transition.at))
			}
		}
		for _, transition := range values {
			buffer.WriteByte(transition.typeIndex)
		}
		for _, info := range []struct {
			offset int32
			isdst  byte
			abbr   byte
		}{{standardOffset, 0, 0}, {daylightOffset, 1, 4}} {
			_ = binary.Write(buffer, binary.BigEndian, info.offset)
			buffer.WriteByte(info.isdst)
			buffer.WriteByte(info.abbr)
		}
		buffer.Write(abbreviations)
	}
	var buffer bytes.Buffer
	// v1 区块不携带可能溢出 int32 的转换；真正的完整时间线放在后续 64 位区块。
	writeHeader(&buffer, '2', 0)
	writeData(&buffer, false, nil)
	writeHeader(&buffer, '2', len(transitions))
	writeData(&buffer, true, transitions)
	// 无 POSIX 规则时仍需写入空的双换行结尾，避免解析器把 TZif 截断视为损坏。
	buffer.WriteString("\n\n")
	return buffer.Bytes()
}
