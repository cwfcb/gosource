package context

import (
	"context"
	"testing"
	"time"
)

// go test -v example_test.go
func TestAfterFunc(t *testing.T) {
	t.Run("automation clean resource after timeout", func(t *testing.T) {
		clean := func() {
			t.Log("clean resource")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = context.AfterFunc(ctx, clean)
		time.Sleep(2 * time.Second)
	}) // output: clean resource

	t.Run("stop run clean func", func(t *testing.T) {
		clean := func() {
			t.Log("clean resource")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		stop := context.AfterFunc(ctx, clean)
		time.Sleep(1 * time.Second)
		stoped := stop() // stop call registed func
		if stoped {
			t.Log("already stopped")
		} else {
			t.Log("not stoped")
		}
	}) // output: already stopped

	t.Run("ouput log when timeout", func(t *testing.T) {
		output := func() {
			t.Log("Rolling back transaction due to timeout")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		context.AfterFunc(ctx, output)
		time.Sleep(2 * time.Second)
	}) // output: Rolling back transaction due to timeout

}

