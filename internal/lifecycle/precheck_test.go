// Copyright (c) 2026 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx/internal/fxclock"
	"go.uber.org/multierr"
)

func TestPrecheck(t *testing.T) {
	t.Parallel()

	t.Run("NilContext", func(t *testing.T) {
		t.Parallel()

		l := New(testLogger(t), fxclock.System)
		err := l.Precheck(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil context")
	})

	t.Run("NoChecks", func(t *testing.T) {
		t.Parallel()

		l := New(testLogger(t), fxclock.System)
		assert.NoError(t, l.Precheck(context.Background()))
	})

	t.Run("ExecutesInOrder", func(t *testing.T) {
		t.Parallel()

		l := New(testLogger(t), fxclock.System)
		var order []string

		l.AppendPrecheck(PrecheckHook{
			Name: "first",
			OnCheck: func(context.Context) error {
				order = append(order, "first")
				return nil
			},
		})
		l.AppendPrecheck(PrecheckHook{
			Name: "second",
			OnCheck: func(context.Context) error {
				order = append(order, "second")
				return nil
			},
		})
		l.AppendPrecheck(PrecheckHook{
			Name: "third",
			OnCheck: func(context.Context) error {
				order = append(order, "third")
				return nil
			},
		})

		require.NoError(t, l.Precheck(context.Background()))
		assert.Equal(t, []string{"first", "second", "third"}, order)
	})

	t.Run("NilOnCheckSkipped", func(t *testing.T) {
		t.Parallel()

		l := New(testLogger(t), fxclock.System)
		var ran int32
		l.AppendPrecheck(PrecheckHook{Name: "nil"})
		l.AppendPrecheck(PrecheckHook{
			Name: "real",
			OnCheck: func(context.Context) error {
				atomic.AddInt32(&ran, 1)
				return nil
			},
		})

		require.NoError(t, l.Precheck(context.Background()))
		assert.Equal(t, int32(1), atomic.LoadInt32(&ran))
	})

	t.Run("AggregatesFailuresWithNameAndCause", func(t *testing.T) {
		t.Parallel()

		l := New(testLogger(t), fxclock.System)

		errBoom := errors.New("boom")
		errKapow := errors.New("kapow")

		var ran int32
		l.AppendPrecheck(PrecheckHook{
			Name: "failing-one",
			OnCheck: func(context.Context) error {
				atomic.AddInt32(&ran, 1)
				return errBoom
			},
		})
		l.AppendPrecheck(PrecheckHook{
			Name: "passing",
			OnCheck: func(context.Context) error {
				atomic.AddInt32(&ran, 1)
				return nil
			},
		})
		l.AppendPrecheck(PrecheckHook{
			Name: "failing-two",
			OnCheck: func(context.Context) error {
				atomic.AddInt32(&ran, 1)
				return fmt.Errorf("wrap: %w", errKapow)
			},
		})

		err := l.Precheck(context.Background())
		require.Error(t, err)

		// Non-cancellation failures don't short-circuit: every check runs.
		assert.Equal(t, int32(3), atomic.LoadInt32(&ran))

		leafs := multierr.Errors(err)
		require.Len(t, leafs, 2)

		var perr *PrecheckError
		require.True(t, errors.As(leafs[0], &perr))
		assert.Equal(t, "failing-one", perr.Name)
		assert.ErrorIs(t, perr, errBoom)

		require.True(t, errors.As(leafs[1], &perr))
		assert.Equal(t, "failing-two", perr.Name)
		assert.ErrorIs(t, perr, errKapow)

		assert.Contains(t, err.Error(), `precheck "failing-one" failed: boom`)
		assert.Contains(t, err.Error(), `precheck "failing-two" failed: wrap: kapow`)
	})

	t.Run("SingleFailure", func(t *testing.T) {
		t.Parallel()

		l := New(testLogger(t), fxclock.System)
		errBoom := errors.New("boom")
		l.AppendPrecheck(PrecheckHook{
			Name: "only",
			OnCheck: func(context.Context) error {
				return errBoom
			},
		})

		err := l.Precheck(context.Background())
		require.Error(t, err)

		var perr *PrecheckError
		require.True(t, errors.As(err, &perr), "expected *PrecheckError, got %T", err)
		assert.Equal(t, "only", perr.Name)
		assert.ErrorIs(t, err, errBoom)
	})

	t.Run("CanceledContextSkipsRemaining", func(t *testing.T) {
		t.Parallel()

		l := New(testLogger(t), fxclock.System)
		var ran int32
		l.AppendPrecheck(PrecheckHook{
			Name: "would-run",
			OnCheck: func(context.Context) error {
				atomic.AddInt32(&ran, 1)
				return nil
			},
		})
		l.AppendPrecheck(PrecheckHook{
			Name: "must-not-run",
			OnCheck: func(context.Context) error {
				atomic.AddInt32(&ran, 1)
				t.Error("this check must not run after cancellation")
				return nil
			},
		})

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := l.Precheck(ctx)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(0), atomic.LoadInt32(&ran))
	})

	t.Run("CheckReturningCanceledSkipsRemaining", func(t *testing.T) {
		t.Parallel()

		l := New(testLogger(t), fxclock.System)
		var ran int32

		ctx, cancel := context.WithCancel(context.Background())
		l.AppendPrecheck(PrecheckHook{
			Name: "triggers-cancel",
			OnCheck: func(context.Context) error {
				atomic.AddInt32(&ran, 1)
				cancel()
				return context.Canceled
			},
		})
		l.AppendPrecheck(PrecheckHook{
			Name: "must-not-run",
			OnCheck: func(context.Context) error {
				atomic.AddInt32(&ran, 1)
				t.Error("this check must not run after a cancellation error")
				return nil
			},
		})

		err := l.Precheck(ctx)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(1), atomic.LoadInt32(&ran))
	})

	t.Run("CheckReturningDeadlineExceededSkipsRemaining", func(t *testing.T) {
		t.Parallel()

		l := New(testLogger(t), fxclock.System)
		var ran int32
		l.AppendPrecheck(PrecheckHook{
			Name: "times-out",
			OnCheck: func(context.Context) error {
				atomic.AddInt32(&ran, 1)
				return fmt.Errorf("slow: %w", context.DeadlineExceeded)
			},
		})
		l.AppendPrecheck(PrecheckHook{
			Name: "must-not-run",
			OnCheck: func(context.Context) error {
				atomic.AddInt32(&ran, 1)
				return nil
			},
		})

		err := l.Precheck(context.Background())
		require.Error(t, err)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, int32(1), atomic.LoadInt32(&ran))
	})

	t.Run("CancellationAfterFailureCombinesErrors", func(t *testing.T) {
		t.Parallel()

		l := New(testLogger(t), fxclock.System)
		errBoom := errors.New("boom")

		ctx, cancel := context.WithCancel(context.Background())

		l.AppendPrecheck(PrecheckHook{
			Name:    "fails",
			OnCheck: func(context.Context) error { return errBoom },
		})
		l.AppendPrecheck(PrecheckHook{
			Name: "cancels",
			OnCheck: func(context.Context) error {
				cancel()
				return nil
			},
		})
		l.AppendPrecheck(PrecheckHook{
			Name: "must-not-run",
			OnCheck: func(context.Context) error {
				t.Error("this check must not run after cancellation")
				return nil
			},
		})

		err := l.Precheck(ctx)
		require.Error(t, err)
		assert.ErrorIs(t, err, errBoom)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("NameFallbacks", func(t *testing.T) {
		t.Parallel()

		t.Run("WrappedFunctionName", func(t *testing.T) {
			t.Parallel()

			l := New(testLogger(t), fxclock.System)
			l.AppendPrecheck(PrecheckHook{
				OnCheck:     precheckNamedCheck,
				OnCheckName: "wrappedCheckName",
			})

			var perr *PrecheckError
			require.True(t, errors.As(l.Precheck(context.Background()), &perr))
			assert.Equal(t, "wrappedCheckName", perr.Name)
		})

		t.Run("FunctionName", func(t *testing.T) {
			t.Parallel()

			l := New(testLogger(t), fxclock.System)
			l.AppendPrecheck(PrecheckHook{OnCheck: precheckNamedCheck})

			var perr *PrecheckError
			require.True(t, errors.As(l.Precheck(context.Background()), &perr))
			assert.Contains(t, perr.Name, "precheckNamedCheck")
		})
	})

	t.Run("ReceivesContextDeadline", func(t *testing.T) {
		t.Parallel()

		l := New(testLogger(t), fxclock.System)
		wantDeadline := time.Now().Add(time.Hour)

		var gotDeadline time.Time
		l.AppendPrecheck(PrecheckHook{
			Name: "deadline",
			OnCheck: func(ctx context.Context) error {
				var ok bool
				gotDeadline, ok = ctx.Deadline()
				assert.True(t, ok, "expected a deadline on the context")
				return nil
			},
		})

		ctx, cancel := context.WithDeadline(context.Background(), wantDeadline)
		defer cancel()

		require.NoError(t, l.Precheck(ctx))
		assert.Equal(t, wantDeadline, gotDeadline)
	})

	t.Run("RetryRunsAllChecksAgain", func(t *testing.T) {
		t.Parallel()

		l := New(testLogger(t), fxclock.System)

		var calls int32
		var failNext int32 = 1
		l.AppendPrecheck(PrecheckHook{
			Name: "flaky",
			OnCheck: func(context.Context) error {
				n := atomic.AddInt32(&calls, 1)
				if atomic.LoadInt32(&failNext) != 0 {
					return fmt.Errorf("attempt %d failed", n)
				}
				return nil
			},
		})

		// First attempt fails and leaves the lifecycle ready to retry.
		require.Error(t, l.Precheck(context.Background()))
		assert.Equal(t, int32(1), atomic.LoadInt32(&calls))

		// Results must not be cached: a second attempt re-runs the check.
		atomic.StoreInt32(&failNext, 0)
		require.NoError(t, l.Precheck(context.Background()))
		assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
	})

	t.Run("FailureDoesNotStartOrStopHooks", func(t *testing.T) {
		t.Parallel()

		l := New(testLogger(t), fxclock.System)

		var started, stopped int32
		l.AppendPrecheck(PrecheckHook{
			Name: "fails",
			OnCheck: func(context.Context) error {
				return errors.New("nope")
			},
		})
		l.Append(Hook{
			OnStart: func(context.Context) error {
				atomic.AddInt32(&started, 1)
				return nil
			},
			OnStop: func(context.Context) error {
				atomic.AddInt32(&stopped, 1)
				return nil
			},
		})

		require.Error(t, l.Precheck(context.Background()))

		// The lifecycle remains stopped: Start can still run and Stop is a
		// no-op without ever invoking OnStart.
		require.NoError(t, l.Start(context.Background()))
		assert.Equal(t, int32(1), atomic.LoadInt32(&started))
		require.NoError(t, l.Stop(context.Background()))
		assert.Equal(t, int32(1), atomic.LoadInt32(&stopped))
	})
}

func precheckNamedCheck(context.Context) error {
	return errors.New("precheckNamedCheck failure")
}
