package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/harshwagh21/reliq/pkg/chain"
	"github.com/harshwagh21/reliq/pkg/retry"
)

func main() {
	attempt := 0
	// One nanosecond is enough to show a retry. The wait is at most that long.
	r, err := retry.New(retry.Config{
		MaxAttempts: 2,
		BaseDelay:   time.Nanosecond,
		MaxDelay:    time.Nanosecond,
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	err = chain.Do(context.Background(), func(context.Context) error {
		attempt++
		if attempt == 1 {
			return errors.New("temporary")
		}
		return nil
	}, r.Policy())
	fmt.Println(err == nil)
}
