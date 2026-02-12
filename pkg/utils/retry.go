package utils

import (
	"context"
	"strings"
)

func IsRetryAble(ctx context.Context, err error) bool {
	info := strings.ToLower(err.Error())
	if strings.Contains(info, "too many requests") {
		return true
	}
	if strings.Contains(info, "status code: 429") {
		return true
	}
	return false
}
