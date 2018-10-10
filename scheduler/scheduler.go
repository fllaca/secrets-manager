package scheduler

import (
	"context"
	"time"
)

type ScheduledFunc func()

func Schedule(ctx context.Context, period time.Duration, funcToSchedule ScheduledFunc) {
	go func(ctx context.Context) {
		for {
			select {
			case <-time.After(period):
				funcToSchedule()
			case <-ctx.Done():
				return
			}
		}
	}(ctx)
}