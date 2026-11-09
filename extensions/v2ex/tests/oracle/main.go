package main

import (
	"encoding/json"
	"fmt"
	"time"
)

func main() {
	var rows []map[string]string
	for _, sec := range []int64{8640000000000, 8640000000001, 9000000000000, -9000000000000, 9007199254740991, -9007199254740991, 9007199254740992, 9007199254740993, -9007199254740993, 9223372036854775807, 9223372036854775806, -9223372036854775808, -9223372036854775807, 8113015806, 8113015807, 8113015808} {
		for _, zone := range []string{"UTC", "Asia/Shanghai", "Asia/Tokyo", "America/New_York", "Europe/London"} {
			loc, err := time.LoadLocation(zone)
			if err != nil {
				panic(err)
			}
			rows = append(rows, map[string]string{"seconds": fmt.Sprint(sec), "zone": zone, "formatted": time.Unix(sec, 0).In(loc).Format("01-02 15:04"), "age_days": fmt.Sprint(int(time.UnixMilli(1700000000000).Sub(time.Unix(sec, 0)).Hours() / 24))})
		}
	}
	b, err := json.Marshal(rows)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))
}
