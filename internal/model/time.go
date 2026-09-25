package model

import "time"

var beijingLocation = time.FixedZone("UTC+8", 8*60*60)

// BeijingLocation 返回固定的北京时间时区，偏移为 UTC+8，不采用夏令时。
func BeijingLocation() *time.Location {
	return beijingLocation
}
