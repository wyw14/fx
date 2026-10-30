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

package fx

import (
	"context"

	"go.uber.org/fx/internal/lifecycle"
)

// Prechecker allows constructors to register callbacks that verify runtime
// conditions before the application runs any OnStart hooks.
//
// Prechecker is available from the Fx dependency injection graph, just like
// [Lifecycle]. Take a dependency on it from a constructor or [Invoke]
// function to register pre-checks:
//
//	fx.Provide(func(p fx.Prechecker, db *sql.DB) *Server {
//		p.Append(fx.CheckHook("database", db.PingContext))
//		return &Server{db: db}
//	})
//
// Pre-checks run during [App.Start], after all constructors and [Invoke]
// functions have completed and before any [Hook.OnStart] callback. They run
// one at a time in registration order, sharing the startup context and its
// deadline. Non-cancellation errors from all checks are combined into a
// single error; if any check fails, no OnStart hooks run and no OnStop hooks
// run. If the startup context is canceled (or a check returns a
// cancellation error), remaining checks are skipped.
//
// Pre-checks are not a replacement for constructors: dependency construction
// and graph validation still happen in [New]. Pre-checks are for conditions
// that can only be evaluated at startup time, such as network reachability or
// external configuration. Pre-checks should only inspect conditions and must
// not mutate application state: Fx does not undo any side effects that a
// pre-check callback performs.
//
// Pre-check results are never cached. Retrying [App.Start] after a failure
// re-runs every registered check against the new startup context.
type Prechecker interface {
	// Append registers a pre-check callback to run before application
	// startup.
	Append(PrecheckHook)
}

// A PrecheckHook is a callback executed during the pre-check phase that runs
// before OnStart hooks.
type PrecheckHook struct {
	// Name identifies the check in returned errors. If empty, the
	// check function's name is used.
	Name string

	// OnCheck verifies a single runtime condition. It receives the
	// application startup context, including its deadline, and returns
	// an error if the condition is not met.
	//
	// OnCheck should only inspect conditions. Any side effects it
	// performs are not rolled back when startup fails.
	OnCheck func(context.Context) error

	onCheckName string
}

// CheckHook returns a [PrecheckHook] with the given name and check callback,
// wrapping the callback's signature as needed. For example:
//
//	p.Append(fx.CheckHook("config", func() error {
//		if cfg.Addr == "" {
//			return errors.New("missing address")
//		}
//		return nil
//	}))
//
// The same function signatures supported by [StartHook] are supported:
// func(), func() error, func(context.Context), and
// func(context.Context) error. If name is empty, the function name is used
// in error messages.
func CheckHook[T HookFunc](name string, check T) PrecheckHook {
	oncheck, checkname := lifecycle.Wrap(check)

	return PrecheckHook{
		Name:        name,
		OnCheck:     oncheck,
		onCheckName: checkname,
	}
}

// PrecheckError reports that a single pre-check failed during application
// startup.
//
// The combined error returned by [App.Start] contains one PrecheckError per
// failed check. Use [errors.As] to inspect the check names and [errors.Is]
// to match the wrapped cause.
type PrecheckError = lifecycle.PrecheckError

// precheckWrapper adapts the internal lifecycle pre-check registry to the
// public [Prechecker] interface, mirroring [lifecycleWrapper].
type precheckWrapper struct {
	*lifecycle.Lifecycle
}

func (p *precheckWrapper) Append(h PrecheckHook) {
	p.Lifecycle.AppendPrecheck(lifecycle.PrecheckHook{
		Name:        h.Name,
		OnCheck:     h.OnCheck,
		OnCheckName: h.onCheckName,
	})
}
