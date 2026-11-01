// Package queryfeedback carries management-only transport retry metadata.
package queryfeedback

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type key struct{}

func WithRetryReceiver(ctx context.Context, receive func(time.Time)) context.Context {
	return context.WithValue(ctx, key{}, receive)
}
func RecordRetry(ctx context.Context, value string) {
	receive, _ := ctx.Value(key{}).(func(time.Time))
	if receive == nil {
		return
	}
	now := time.Now()
	retry := now.Add(time.Minute)
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 && seconds <= 315360000 {
		retry = now.Add(time.Duration(seconds) * time.Second)
	} else if date, err := http.ParseTime(value); err == nil && date.After(now) {
		retry = date
	}
	receive(retry)
}
