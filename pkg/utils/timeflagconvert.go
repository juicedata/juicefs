package utils

import (
	"fmt"
	"strconv"
	"strings"
)

// UploadDelayTimeLimtFlags time like 10:20 or just 10
func UploadDelayTimeLimtFlags(startuploadStr, enduploadStr string) (startHour, startMinute, endHour, endMinute int) {
	var err error
	if startuploadStr != "" && enduploadStr != "" {
		startParts := strings.Split(startuploadStr, ":")
		endParts := strings.Split(enduploadStr, ":")
		if len(startParts) == 1 {
			startHour, err = strconv.Atoi(startParts[0])
			if err != nil {
				fmt.Println("Please pass in the correct time period parameters.")
			}
			startMinute = 0
		} else {
			startHour, err = strconv.Atoi(startParts[0])
			if err != nil {
				fmt.Println("Please pass in the correct time period parameters.")
			}
			startMinute, err = strconv.Atoi(startParts[1])
			if err != nil {
				fmt.Println("Please pass in the correct time period parameters.")
			}
		}
		if len(endParts) == 1 {
			endHour, err = strconv.Atoi(endParts[0])
			if err != nil {
				fmt.Println("Please pass in the correct time period parameters.")
			}
			endMinute = 0
		} else {
			endHour, err = strconv.Atoi(endParts[0])
			if err != nil {
				fmt.Println("Please pass in the correct time period parameters.")
			}
			endMinute, err = strconv.Atoi(endParts[1])
			if err != nil {
				fmt.Println("Please pass in the correct time period parameters.")
			}
		}
	}
	return startHour, startMinute, endHour, endMinute
}
