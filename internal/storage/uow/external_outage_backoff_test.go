package uow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	mysql "github.com/go-sql-driver/mysql"
)

type outageTestPinger func(context.Context) error

func (p outageTestPinger) PingContext(ctx context.Context) error { return p(ctx) }

func TestExternalOutageUsesBoundedRetryWithoutSpinning(t *testing.T) {
	// Use the public wire shape rather than a new helper so this compiles
	// against the fork before the proxy and retry changes are in place.
	outage := &mysql.MySQLError{Number: 2003, Message: "beads db proxy: upstream Dolt server unavailable"}
	newBackoff := func() *backoff.ExponentialBackOff {
		bo := testPingBackOff()
		bo.RandomizationFactor = 0
		bo.InitialInterval = 100 * time.Millisecond
		bo.MaxInterval = 100 * time.Millisecond
		bo.MaxElapsedTime = 4 * time.Second
		return bo
	}

	t.Run("persistent outage stops before the ordinary budget", func(t *testing.T) {
		calls := 0
		start := time.Now()
		p := outageTestPinger(func(context.Context) error {
			calls++
			return outage
		})
		err := pingWithRetry(t.Context(), p, newBackoff(), testPingAttemptTimeout)
		if !errors.Is(err, outage) || calls < 2 || time.Since(start) > 3*time.Second {
			t.Errorf("persistent outage returned %v after %d attempts in %s, want bounded retries", err, calls, time.Since(start))
		}
	})

	t.Run("ordinary transient errors keep their intervals after the outage window", func(t *testing.T) {
		var calls int
		var start time.Time
		p := outageTestPinger(func(context.Context) error {
			calls++
			if calls == 1 {
				start = time.Now()
				return outage
			}
			if time.Since(start) < 1350*time.Millisecond {
				return mysql.ErrInvalidConn
			}
			return nil
		})
		if err := pingWithRetry(t.Context(), p, newBackoff(), testPingAttemptTimeout); err != nil {
			t.Fatalf("ping never recovered: %v (attempts %d)", err, calls)
		}
		if calls > 40 {
			t.Errorf("ordinary errors spun after the outage window: %d attempts", calls)
		}
	})
}
