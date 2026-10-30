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

package fx_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/fx/fxtest"
	"go.uber.org/multierr"
)

func TestPrecheckProvides(t *testing.T) {
	t.Parallel()

	t.Run("AvailableFromDI", func(t *testing.T) {
		t.Parallel()

		var p Prechecker
		fxtest.New(
			t,
			Populate(&p),
		)
		assert.NotNil(t, p, "Prechecker should be provided by the application")
	})

	t.Run("DistinctFromLifecycle", func(t *testing.T) {
		t.Parallel()

		var (
			l Lifecycle
			p Prechecker
		)
		fxtest.New(
			t,
			Populate(&l, &p),
		)
		assert.NotNil(t, l)
		assert.NotNil(t, p)
	})
}

func TestPrecheckRunsBeforeOnStart(t *testing.T) {
	t.Parallel()

	var order []string

	type dep struct{}
	type svc struct{}

	app := NewForTest(t,
		Provide(func(p Prechecker) *dep {
			p.Append(CheckHook("check-dep", func(context.Context) error {
				order = append(order, "check-dep")
				return nil
			}))
			return &dep{}
		}),
		Provide(func(p Prechecker, d *dep) svc {
			p.Append(CheckHook("check-svc", func(context.Context) error {
				order = append(order, "check-svc")
				return nil
			}))
			return svc{}
		}),
		Invoke(func(lc Lifecycle, s svc) {
			lc.Append(Hook{
				OnStart: func(context.Context) error {
					order = append(order, "onstart")
					return nil
				},
			})
		}),
	)

	require.NoError(t, app.Start(context.Background()))
	assert.Equal(t,
		[]string{"check-dep", "check-svc", "onstart"},
		order,
		"all pre-checks must run before the first OnStart hook")
	require.NoError(t, app.Stop(context.Background()))
}

func TestPrecheckFailureSkipsHooks(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")
	var started, stopped int32

	app, spy := NewSpied(
		Invoke(func(p Prechecker, lc Lifecycle) {
			p.Append(CheckHook("failing-check", func(context.Context) error {
				return errBoom
			}))
			lc.Append(Hook{
				OnStart: func(context.Context) error {
					atomic.AddInt32(&started, 1)
					return nil
				},
				OnStop: func(context.Context) error {
					atomic.AddInt32(&stopped, 1)
					return nil
				},
			})
		}),
	)

	err := app.Start(context.Background())
	require.Error(t, err)

	// The error carries the check name and the original cause.
	var perr *PrecheckError
	require.True(t, errors.As(err, &perr), "expected *fx.PrecheckError, got %v", err)
	assert.Equal(t, "failing-check", perr.Name)
	assert.ErrorIs(t, err, errBoom)
	assert.Contains(t, err.Error(), `precheck "failing-check" failed: boom`)

	assert.Equal(t, int32(0), atomic.LoadInt32(&started),
		"no OnStart hook must run when a pre-check fails")

	// Explicit Stop must not run OnStop for hooks that never started.
	require.NoError(t, app.Stop(context.Background()))
	assert.Equal(t, int32(0), atomic.LoadInt32(&stopped),
		"no OnStop hook must run when OnStart was never called")

	// Failed pre-checks are not rolled back: the app never entered the
	// OnStart phase, so no RollingBack/RolledBack events are emitted.
	for _, ev := range spy.Events() {
		switch ev.(type) {
		case *fxevent.RollingBack, *fxevent.RolledBack:
			t.Fatalf("unexpected rollback event for failed pre-check: %#v", ev)
		case *fxevent.Started:
			assert.Equal(t, err, ev.(*fxevent.Started).Err)
		}
	}
}

func TestPrecheckAggregatesFailures(t *testing.T) {
	t.Parallel()

	errOne := errors.New("one")
	errTwo := errors.New("two")

	var ran int32
	app := NewForTest(t,
		Invoke(func(p Prechecker) {
			p.Append(PrecheckHook{
				Name: "check-one",
				OnCheck: func(context.Context) error {
					atomic.AddInt32(&ran, 1)
					return errOne
				},
			})
			p.Append(PrecheckHook{
				Name: "check-two",
				OnCheck: func(context.Context) error {
					atomic.AddInt32(&ran, 1)
					return nil
				},
			})
			p.Append(CheckHook("check-three", func() error {
				atomic.AddInt32(&ran, 1)
				return errTwo
			}))
		}),
	)

	err := app.Start(context.Background())
	require.Error(t, err)
	assert.Equal(t, int32(3), atomic.LoadInt32(&ran),
		"non-cancellation errors must not short-circuit remaining checks")

	leafs := multierr.Errors(err)
	require.Len(t, leafs, 2)

	var perr *PrecheckError
	require.True(t, errors.As(leafs[0], &perr))
	assert.Equal(t, "check-one", perr.Name)
	assert.ErrorIs(t, leafs[0], errOne)

	require.True(t, errors.As(leafs[1], &perr))
	assert.Equal(t, "check-three", perr.Name)
	assert.ErrorIs(t, leafs[1], errTwo)

	require.NoError(t, app.Stop(context.Background()))
}

func TestPrecheckCancellation(t *testing.T) {
	t.Parallel()

	t.Run("CanceledContextSkipsChecksAndHooks", func(t *testing.T) {
		t.Parallel()

		var checksRan, started int32
		app := NewForTest(t,
			Invoke(func(p Prechecker, lc Lifecycle) {
				p.Append(CheckHook("must-not-run", func(context.Context) error {
					atomic.AddInt32(&checksRan, 1)
					return nil
				}))
				lc.Append(Hook{
					OnStart: func(context.Context) error {
						atomic.AddInt32(&started, 1)
						return nil
					},
				})
			}),
		)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := app.Start(ctx)
		require.ErrorIs(t, err, context.Canceled)

		// Give the start worker goroutine a moment to settle: it must
		// never reach the check or the hook.
		time.Sleep(50 * time.Millisecond)
		assert.Equal(t, int32(0), atomic.LoadInt32(&checksRan))
		assert.Equal(t, int32(0), atomic.LoadInt32(&started))

		require.NoError(t, app.Stop(context.Background()))
	})

	t.Run("CancellationBetweenChecksSkipsRemaining", func(t *testing.T) {
		t.Parallel()

		var laterRan int32
		firstStarted := make(chan struct{})

		ctx, cancel := context.WithCancel(context.Background())
		app := NewForTest(t,
			Invoke(func(p Prechecker) {
				p.Append(CheckHook("first", func(context.Context) error {
					close(firstStarted)
					// Wait until the test cancels the shared startup
					// context, then return cleanly: the next check must
					// still be skipped.
					<-ctx.Done()
					return nil
				}))
				p.Append(CheckHook("later", func(context.Context) error {
					atomic.AddInt32(&laterRan, 1)
					return nil
				}))
			}),
		)

		go func() {
			<-firstStarted
			cancel()
		}()

		err := app.Start(ctx)
		require.ErrorIs(t, err, context.Canceled)

		time.Sleep(50 * time.Millisecond)
		assert.Equal(t, int32(0), atomic.LoadInt32(&laterRan),
			"checks after cancellation must not run")
	})
}

func TestPrecheckSharesStartupContext(t *testing.T) {
	t.Parallel()

	type ctxKey struct{}

	var (
		checkValue, startValue any
		checkDeadlineOK        bool
	)

	deadline := time.Now().Add(time.Hour)

	app := NewForTest(t,
		Invoke(func(p Prechecker, lc Lifecycle) {
			p.Append(CheckHook("ctx", func(ctx context.Context) error {
				checkValue = ctx.Value(ctxKey{})
				_, checkDeadlineOK = ctx.Deadline()
				return nil
			}))
			lc.Append(Hook{
				OnStart: func(ctx context.Context) error {
					startValue = ctx.Value(ctxKey{})
					return nil
				},
			})
		}),
	)

	ctx, cancel := context.WithDeadline(
		context.WithValue(context.Background(), ctxKey{}, "shared"),
		deadline,
	)
	defer cancel()

	require.NoError(t, app.Start(ctx))
	assert.Equal(t, "shared", checkValue, "pre-check must receive the startup context")
	assert.True(t, checkDeadlineOK, "pre-check must receive the startup deadline")
	assert.Equal(t, "shared", startValue, "OnStart must receive the same startup context")

	require.NoError(t, app.Stop(context.Background()))
}

func TestPrecheckRerunsOnRetry(t *testing.T) {
	t.Parallel()

	var (
		checkCalls int32
		started    int32
		stopped    int32
		shouldFail int32 = 1
	)

	app := NewForTest(t,
		Invoke(func(p Prechecker, lc Lifecycle) {
			p.Append(CheckHook("flaky", func(context.Context) error {
				atomic.AddInt32(&checkCalls, 1)
				if atomic.LoadInt32(&shouldFail) != 0 {
					return errors.New("condition not ready")
				}
				return nil
			}))
			lc.Append(Hook{
				OnStart: func(context.Context) error {
					atomic.AddInt32(&started, 1)
					return nil
				},
				OnStop: func(context.Context) error {
					atomic.AddInt32(&stopped, 1)
					return nil
				},
			})
		}),
	)

	// First attempt: the pre-check fails; nothing starts.
	err := app.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), `precheck "flaky" failed: condition not ready`)
	assert.Equal(t, int32(1), atomic.LoadInt32(&checkCalls))
	assert.Equal(t, int32(0), atomic.LoadInt32(&started))

	// Once the condition is satisfied, retrying Start must re-run the
	// checks rather than reuse the previous failure.
	atomic.StoreInt32(&shouldFail, 0)
	require.NoError(t, app.Start(context.Background()))
	assert.Equal(t, int32(2), atomic.LoadInt32(&checkCalls))
	assert.Equal(t, int32(1), atomic.LoadInt32(&started))

	require.NoError(t, app.Stop(context.Background()))
	assert.Equal(t, int32(1), atomic.LoadInt32(&stopped))
}

func TestCheckHookHelper(t *testing.T) {
	t.Parallel()

	t.Run("ExplicitName", func(t *testing.T) {
		t.Parallel()

		errBoom := errors.New("helper boom")
		app := NewForTest(t,
			Invoke(func(p Prechecker) {
				// func() error is one of the HookFunc signatures supported
				// by CheckHook.
				p.Append(CheckHook("helper-check", func() error {
					return errBoom
				}))
			}),
		)

		err := app.Start(context.Background())
		require.Error(t, err)

		var perr *PrecheckError
		require.True(t, errors.As(err, &perr))
		assert.Equal(t, "helper-check", perr.Name)
		assert.ErrorIs(t, err, errBoom)
	})

	t.Run("FallbackFunctionName", func(t *testing.T) {
		t.Parallel()

		app := NewForTest(t,
			Invoke(func(p Prechecker) {
				p.Append(CheckHook("", func(ctx context.Context) error {
					return errors.New("no explicit name")
				}))
			}),
		)

		err := app.Start(context.Background())
		require.Error(t, err)

		var perr *PrecheckError
		require.True(t, errors.As(err, &perr))
		assert.NotEmpty(t, perr.Name)
		assert.Contains(t, perr.Name, "func")
		assert.Contains(t, err.Error(), "no explicit name")
	})
}

func TestNoPrechecksKeepsLifecycle(t *testing.T) {
	t.Parallel()

	var started, stopped int32
	app, spy := NewSpied(
		Invoke(func(lc Lifecycle) {
			lc.Append(Hook{
				OnStart: func(context.Context) error {
					atomic.AddInt32(&started, 1)
					return nil
				},
				OnStop: func(context.Context) error {
					atomic.AddInt32(&stopped, 1)
					return nil
				},
			})
		}),
	)

	require.NoError(t, app.Start(context.Background()))
	require.NoError(t, app.Stop(context.Background()))
	assert.Equal(t, int32(1), atomic.LoadInt32(&started))
	assert.Equal(t, int32(1), atomic.LoadInt32(&stopped))

	// The pre-check phase must be invisible to apps that register none.
	assert.NotContains(t, spy.EventTypes(), "RollingBack")
}
