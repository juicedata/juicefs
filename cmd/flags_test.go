package cmd

import (
	"github.com/juicedata/juicefs/pkg/utils"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_duration(t *testing.T) {
	type args struct {
		s string
	}
	tests := []struct {
		name string
		args args
		want time.Duration
	}{
		{
			name: "DurationWithSeconds",
			args: args{s: "60"},
			want: time.Minute,
		},
		{
			name: "DurationWithHours",
			args: args{s: "2h"},
			want: 2 * time.Hour,
		},
		{
			name: "DurationWithDays",
			args: args{s: "1d"},
			want: 24 * time.Hour,
		},
		{
			name: "DurationWithDaysAndTime",
			args: args{s: "1d2h"},
			want: 26 * time.Hour,
		},
		{
			name: "DurationWithInvalidInput",
			args: args{s: "invalid"},
			want: 0,
		},
		{
			name: "DurationWithEmptyString",
			args: args{s: ""},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, utils.Duration(tt.args.s), "duration(%v)", tt.args.s)
		})
	}
}
