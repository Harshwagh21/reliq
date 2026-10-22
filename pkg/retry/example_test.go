package retry_test

import (
	"context"
	"fmt"
	"time"

	"github.com/harshwagh21/reliq/pkg/chain"
	"github.com/harshwagh21/reliq/pkg/retry"
)

func ExampleNew() {
	r, err := retry.New(retry.Config{
		MaxAttempts: 1,
		BaseDelay:   time.Millisecond,
		MaxDelay:    time.Millisecond,
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	err = chain.Do(context.Background(), func(context.Context) error {
		return nil
	}, r.Policy())
	fmt.Println(err == nil)
	// Output:
	// true
}
