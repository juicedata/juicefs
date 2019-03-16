package obs

import (
	"encoding/xml"
	"fmt"
)

type ObsError struct {
	BaseModel
	Status   string
	XMLName  xml.Name `xml:"Error"`
	Code     string   `xml:"Code"`
	Message  string   `xml:"Message"`
	Resource string   `xml:"Resource"`
	HostId   string   `xml:"HostId"`
}

func (err ObsError) Error() string {
	return fmt.Sprintf("obs: service returned error: Status=%s, Code=%s, Message=%s, RequestId=%s",
		err.Status, err.Code, err.Message, err.RequestId)
}
