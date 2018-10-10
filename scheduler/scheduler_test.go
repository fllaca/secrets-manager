package scheduler

import (
	"context"
	"testing"
	"time"
	"github.com/stretchr/testify/assert"
)

func TestSchedule(t *testing.T){
	ctx := context.Background()
	counter := 1
	Schedule(ctx, 2 * time.Millisecond, func(){
		counter++
	})
	<-time.After(3 * time.Millisecond)

	assert.Equal(t, 2, counter)
}