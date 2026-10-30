// Copyright (c) 2024 Uber Technologies, Inc.
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
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
)

// Types and constructors used across the unused-dependency tests.

type unusedTypeA struct{}
type unusedTypeB struct{}
type unusedTypeC struct{}
type unusedTypeD struct{}
type unusedConn struct {
	Name string
}
type unusedSecret struct{}

func newUnusedA() *unusedTypeA { return &unusedTypeA{} }
func newUnusedB() *unusedTypeB { return &unusedTypeB{} }
func newUnusedAandC() (*unusedTypeA, *unusedTypeC) {
	return &unusedTypeA{}, &unusedTypeC{}
}

type unusedACResult struct {
	fx.Out

	A *unusedTypeA
	C *unusedTypeC
}

func newUnusedACResult() unusedACResult {
	return unusedACResult{A: &unusedTypeA{}, C: &unusedTypeC{}}
}

func newUnusedConnRO() *unusedConn { return &unusedConn{Name: "ro"} }
func newUnusedConnRW() *unusedConn { return &unusedConn{Name: "rw"} }

func newUnusedSecret() *unusedSecret { return &unusedSecret{} }

type unusedHardParam struct {
	fx.In

	Values []string `group:"unusedHard"`
}

func newUnusedHard1() string { return "one" }
func newUnusedHard2() string { return "two" }

type unusedSoftParam struct {
	fx.In

	Values []string `group:"unusedSoft,soft"`
}

type unusedSoftResult struct {
	fx.Out

	Value string `group:"unusedSoft"`
}

func newUnusedSoftMember() (unusedSoftResult, int) {
	return unusedSoftResult{Value: "member"}, 7
}

func newUnusedFloat() float32 { return 3.14 }

type unusedGDParam struct {
	fx.In

	Values []string `group:"unusedGD"`
}

type unusedGDResult struct {
	fx.Out

	Values []string `group:"unusedGD"`
}

func newUnusedGD1() string { return "a" }
func newUnusedGD2() string { return "b" }

func decorateUnusedGD(p unusedGDParam) unusedGDResult {
	out := make([]string, 0, len(p.Values))
	for _, v := range p.Values {
		out = append(out, "decorated:"+v)
	}
	return unusedGDResult{Values: out}
}

// findProviderWithOutput returns the first provider in the list that exposes
// an output whose rendered type contains typeSub.
func findProviderWithOutput(
	t *testing.T,
	providers []fx.UnusedProvider,
	typeSub string,
) (fx.UnusedProvider, fx.UnusedOutput) {
	t.Helper()
	for _, p := range providers {
		for _, o := range p.Outputs {
			if strings.Contains(o.Type, typeSub) {
				return p, o
			}
		}
	}
	t.Fatalf("no provider with output containing %q in %+v", typeSub, providers)
	return fx.UnusedProvider{}, fx.UnusedOutput{}
}

func TestUnusedDependencies_NoneWhenEverythingUsed(t *testing.T) {
	app := fx.New(
		fx.NopLogger,
		fx.Provide(newUnusedA),
		fx.Invoke(func(*unusedTypeA) {}),
	)
	require.NoError(t, app.Err())

	report := app.UnusedDependencies()
	assert.True(t, report.Complete)
	assert.Empty(t, report.Uncalled)
	assert.Empty(t, report.Unconsumed)
	assert.Contains(t, report.String(), "no unused dependencies")
}

func TestUnusedDependencies_NeverCalled(t *testing.T) {
	app := fx.New(
		fx.NopLogger,
		fx.Provide(newUnusedA, newUnusedB),
		fx.Invoke(func(*unusedTypeA) {}),
	)
	require.NoError(t, app.Err())

	report := app.UnusedDependencies()
	require.True(t, report.Complete)
	require.Len(t, report.Uncalled, 1)
	assert.Empty(t, report.Unconsumed)

	p, o := findProviderWithOutput(t, report.Uncalled, "unusedTypeB")
	assert.Contains(t, p.Name, "newUnusedB")
	assert.Equal(t, "", p.Module)
	assert.Contains(t, o.Type, "unusedTypeB")
	assert.Empty(t, o.Group)
	assert.Empty(t, o.Name)
	assert.Contains(t, report.String(), "never called")
}

func TestUnusedDependencies_CalledWithUnconsumedOutput(t *testing.T) {
	t.Run("positional multiple outputs", func(t *testing.T) {
		app := fx.New(
			fx.NopLogger,
			fx.Provide(newUnusedAandC),
			fx.Invoke(func(*unusedTypeA) {}),
		)
		require.NoError(t, app.Err())

		report := app.UnusedDependencies()
		require.True(t, report.Complete)
		assert.Empty(t, report.Uncalled)
		require.Len(t, report.Unconsumed, 1)

		provider := report.Unconsumed[0]
		assert.Contains(t, provider.Name, "newUnusedAandC")
		require.Len(t, provider.Outputs, 1)
		assert.Contains(t, provider.Outputs[0].Type, "unusedTypeC")
	})

	t.Run("out result object", func(t *testing.T) {
		app := fx.New(
			fx.NopLogger,
			fx.Provide(newUnusedACResult),
			fx.Invoke(func(*unusedTypeA) {}),
		)
		require.NoError(t, app.Err())

		report := app.UnusedDependencies()
		require.True(t, report.Complete)
		require.Len(t, report.Unconsumed, 1)
		require.Len(t, report.Unconsumed[0].Outputs, 1)
		assert.Contains(t, report.Unconsumed[0].Outputs[0].Type, "unusedTypeC")
	})
}

func TestUnusedDependencies_NeverCalledMultiOutputListsEveryOutput(t *testing.T) {
	app := fx.New(
		fx.NopLogger,
		fx.Provide(newUnusedAandC),
		// No invocation at all: nothing is needed.
	)
	require.NoError(t, app.Err())

	report := app.UnusedDependencies()
	require.True(t, report.Complete)
	require.Len(t, report.Uncalled, 1)

	types := report.Uncalled[0].Outputs
	require.Len(t, types, 2)
	assert.Contains(t, types[0].Type+"|"+types[1].Type, "unusedTypeA")
	assert.Contains(t, types[0].Type+"|"+types[1].Type, "unusedTypeC")
}

func TestUnusedDependencies_NamedValues(t *testing.T) {
	type param struct {
		fx.In

		Conn *unusedConn `name:"ro"`
	}

	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			fx.Annotated{Name: "ro", Target: newUnusedConnRO},
			fx.Annotated{Name: "rw", Target: newUnusedConnRW},
		),
		fx.Invoke(func(p param) {}),
	)
	require.NoError(t, app.Err())

	report := app.UnusedDependencies()
	require.True(t, report.Complete)
	require.Len(t, report.Uncalled, 1)

	p, o := findProviderWithOutput(t, report.Uncalled, "unusedConn")
	assert.Contains(t, p.Name, "newUnusedConnRW")
	assert.Equal(t, "rw", o.Name)
	assert.Empty(t, o.Group)
}

func TestUnusedDependencies_PrivateModules(t *testing.T) {
	t.Run("private value used inside its own module is not unused", func(t *testing.T) {
		app := fx.New(
			fx.NopLogger,
			fx.Module("child",
				fx.Provide(newUnusedSecret, fx.Private),
				fx.Invoke(func(*unusedSecret) {}),
			),
		)
		require.NoError(t, app.Err())

		report := app.UnusedDependencies()
		assert.True(t, report.Complete)
		assert.Empty(t, report.Uncalled)
		assert.Empty(t, report.Unconsumed)
	})

	t.Run("private value not visible to a sibling is not marked consumed", func(t *testing.T) {
		app := fx.New(
			fx.NopLogger,
			// Private, unused inside module "p".
			fx.Module("p",
				fx.Provide(newUnusedSecret, fx.Private),
			),
			// Sibling module exports its own *unusedSecret and consumes it.
			fx.Module("q",
				fx.Provide(func() *unusedSecret { return &unusedSecret{} }),
				fx.Invoke(func(*unusedSecret) {}),
			),
		)
		require.NoError(t, app.Err())

		report := app.UnusedDependencies()
		require.True(t, report.Complete)
		require.Len(t, report.Uncalled, 1)

		p, o := findProviderWithOutput(t, report.Uncalled, "unusedSecret")
		assert.Equal(t, "p", p.Module)
		assert.True(t, p.Private)
		assert.Contains(t, o.Type, "unusedSecret")
	})
}

func TestUnusedDependencies_HardValueGroupsPullAllMembers(t *testing.T) {
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			fx.Annotated{Group: "unusedHard", Target: newUnusedHard1},
			fx.Annotated{Group: "unusedHard", Target: newUnusedHard2},
		),
		fx.Invoke(func(p unusedHardParam) {
			assert.ElementsMatch(t, []string{"one", "two"}, p.Values)
		}),
	)
	require.NoError(t, app.Err())

	report := app.UnusedDependencies()
	assert.True(t, report.Complete)
	assert.Empty(t, report.Uncalled)
	assert.Empty(t, report.Unconsumed)
}

func TestUnusedDependencies_SoftValueGroups(t *testing.T) {
	t.Run("member constructor is not pulled by a soft group", func(t *testing.T) {
		app := fx.New(
			fx.NopLogger,
			fx.Provide(newUnusedSoftMember, newUnusedFloat),
			fx.Invoke(func(p unusedSoftParam, f float32) {
				assert.Empty(t, p.Values)
				assert.Equal(t, float32(3.14), f)
			}),
		)
		require.NoError(t, app.Err())

		report := app.UnusedDependencies()
		require.True(t, report.Complete)
		require.Len(t, report.Uncalled, 1)

		provider := report.Uncalled[0]
		assert.Contains(t, provider.Name, "newUnusedSoftMember")

		// The group member output is explained as a soft group member.
		var groupOut *fx.UnusedOutput
		for i := range provider.Outputs {
			if provider.Outputs[i].Group == "unusedSoft" {
				groupOut = &provider.Outputs[i]
			}
		}
		require.NotNil(t, groupOut)
		assert.True(t, groupOut.Soft)
		assert.Contains(t, groupOut.Type, "string")
		assert.Equal(t, "unusedSoft", groupOut.Group)
		assert.Contains(t, groupOut.Note, "soft value group")
		assert.Contains(t, report.String(), "soft value group")

		// The unrelated int output is a plain unused value.
		var hasInt bool
		for _, o := range provider.Outputs {
			if o.Type == "int" && !o.Soft {
				hasInt = true
			}
		}
		assert.True(t, hasInt)
	})

	t.Run("member built for another reason is delivered to the soft group", func(t *testing.T) {
		app := fx.New(
			fx.NopLogger,
			fx.Provide(newUnusedSoftMember),
			// int is requested first so the member constructor runs before the
			// soft value group is built.
			fx.Invoke(func(n int, p unusedSoftParam) {
				assert.Equal(t, 7, n)
				assert.ElementsMatch(t, []string{"member"}, p.Values)
			}),
		)
		require.NoError(t, app.Err())

		report := app.UnusedDependencies()
		assert.True(t, report.Complete)
		assert.Empty(t, report.Uncalled)
		assert.Empty(t, report.Unconsumed)
	})
}

func TestUnusedDependencies_GroupDecoration(t *testing.T) {
	app := fx.New(
		fx.NopLogger,
		fx.Module("decorated",
			fx.Provide(
				fx.Annotated{Group: "unusedGD", Target: newUnusedGD1},
				fx.Annotated{Group: "unusedGD", Target: newUnusedGD2},
			),
			fx.Decorate(decorateUnusedGD),
			fx.Invoke(func(p unusedGDParam) {
				assert.ElementsMatch(t,
					[]string{"decorated:a", "decorated:b"}, p.Values)
			}),
		),
	)
	require.NoError(t, app.Err())

	report := app.UnusedDependencies()
	assert.True(t, report.Complete)
	assert.Empty(t, report.Uncalled)
	assert.Empty(t, report.Unconsumed)
}

func TestUnusedDependencies_FailedAssemblyIsIncomplete(t *testing.T) {
	app := fx.New(
		fx.NopLogger,
		fx.Provide(newUnusedA, newUnusedB),
		// This invocation runs and needs *unusedTypeA.
		fx.Invoke(func(*unusedTypeA) {}),
		// This invocation cannot run because *unusedTypeD is missing.
		fx.Invoke(func(*unusedTypeD) {}),
	)
	require.Error(t, app.Err())

	report := app.UnusedDependencies()
	assert.False(t, report.Complete)
	// Constructors that did not run must not be declared unused.
	assert.Empty(t, report.Uncalled)
	assert.Empty(t, report.Unconsumed)
	assert.Contains(t, report.IncompleteReason, "1 of 2")
	assert.Contains(t, report.String(), "incomplete")
}

func TestUnusedDependencies_QueryDoesNotInvokeOrStartHooks(t *testing.T) {
	var (
		mu        sync.Mutex
		aCalls    int
		bCalls    int
		hookCalls int
	)

	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			func() *unusedTypeA {
				mu.Lock()
				aCalls++
				mu.Unlock()
				return &unusedTypeA{}
			},
			func(lc fx.Lifecycle) *unusedTypeB {
				mu.Lock()
				bCalls++
				mu.Unlock()
				lc.Append(fx.Hook{
					OnStart: func(context.Context) error {
						mu.Lock()
						hookCalls++
						mu.Unlock()
						return nil
					},
				})
				return &unusedTypeB{}
			},
		),
		fx.Invoke(func(*unusedTypeA) {}),
	)
	require.NoError(t, app.Err())

	require.Equal(t, 1, aCalls)
	require.Equal(t, 0, bCalls)
	require.Equal(t, 0, hookCalls)

	// Querying the report, any number of times, must not change the picture.
	_ = app.UnusedDependencies()
	_ = app.UnusedDependencies()
	report := app.UnusedDependencies()

	require.Equal(t, 1, aCalls)
	require.Equal(t, 0, bCalls)
	require.Equal(t, 0, hookCalls)

	require.True(t, report.Complete)
	require.Len(t, report.Uncalled, 1)
	_, o := findProviderWithOutput(t, report.Uncalled, "unusedTypeB")
	assert.NotEmpty(t, o.Type)
}

func TestUnusedDependencies_Supply(t *testing.T) {
	t.Run("supplied value consumed", func(t *testing.T) {
		app := fx.New(
			fx.NopLogger,
			fx.Supply(&unusedTypeA{}),
			fx.Invoke(func(*unusedTypeA) {}),
		)
		require.NoError(t, app.Err())
		report := app.UnusedDependencies()
		assert.Empty(t, report.Uncalled)
	})

	t.Run("supplied value unused", func(t *testing.T) {
		app := fx.New(
			fx.NopLogger,
			fx.Supply(&unusedTypeB{}),
		)
		require.NoError(t, app.Err())
		report := app.UnusedDependencies()
		require.Len(t, report.Uncalled, 1)
		assert.Contains(t, report.Uncalled[0].Name, "fx.Supply")
	})
}

func TestUnusedDependencies_PopulateConsumes(t *testing.T) {
	var a *unusedTypeA
	app := fx.New(
		fx.NopLogger,
		fx.Provide(newUnusedA),
		fx.Populate(&a),
	)
	require.NoError(t, app.Err())
	require.NotNil(t, a)

	report := app.UnusedDependencies()
	assert.True(t, report.Complete)
	assert.Empty(t, report.Uncalled)
	assert.Empty(t, report.Unconsumed)
}

// Ensure the custom (fx.WithLogger) constructor's own dependencies count as
// consumed.
func TestUnusedDependencies_CustomLoggerDepsConsumed(t *testing.T) {
	app := fx.New(
		fx.WithLogger(func() fxevent.Logger { return fxevent.NopLogger }),
		fx.Provide(newUnusedA),
		fx.Invoke(func(*unusedTypeA) {}),
	)
	require.NoError(t, app.Err())
	report := app.UnusedDependencies()
	assert.True(t, report.Complete)
	assert.Empty(t, report.Uncalled)
}

// A constructor whose only consumer itself never ran must still be reported
// uncalled: registration does not count as consumption.
func TestUnusedDependencies_TransitiveChainNotRun(t *testing.T) {
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			newUnusedA,
			func(a *unusedTypeA) *unusedTypeB {
				return &unusedTypeB{}
			},
		),
		// No invocation: neither B nor its dependency A are needed.
	)
	require.NoError(t, app.Err())

	report := app.UnusedDependencies()
	require.True(t, report.Complete)

	types := map[string]bool{}
	for _, p := range report.Uncalled {
		for _, o := range p.Outputs {
			types[o.Type] = true
		}
	}
	assert.Len(t, report.Uncalled, 2)
	assert.True(t, containsType(types, "unusedTypeA"))
	assert.True(t, containsType(types, "unusedTypeB"))
}

func containsType(types map[string]bool, sub string) bool {
	for ty := range types {
		if strings.Contains(ty, sub) {
			return true
		}
	}
	return false
}

// An optional dependency that has a provider is still built and consumed.
func TestUnusedDependencies_OptionalDependencyBuilt(t *testing.T) {
	type param struct {
		fx.In

		A *unusedTypeA `optional:"true"`
	}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(newUnusedA),
		fx.Invoke(func(p param) {
			assert.NotNil(t, p.A)
		}),
	)
	require.NoError(t, app.Err())
	report := app.UnusedDependencies()
	assert.True(t, report.Complete)
	assert.Empty(t, report.Uncalled)
	assert.Empty(t, report.Unconsumed)
}

// A constructor provided as an interface via fx.As reports that interface
// type when unused.
func TestUnusedDependencies_AsInterfaceUnused(t *testing.T) {
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			fx.Annotate(
				func() *strings.Reader { return strings.NewReader("hello") },
				fx.As(new(io.Reader)),
			),
		),
	)
	require.NoError(t, app.Err())

	report := app.UnusedDependencies()
	require.True(t, report.Complete)
	require.Len(t, report.Uncalled, 1)
	_, o := findProviderWithOutput(t, report.Uncalled, "io.Reader")
	assert.Equal(t, "io.Reader", o.Type)
}

// A decorator for a single value consumes the underlying provider.
func TestUnusedDependencies_SingleValueDecoration(t *testing.T) {
	app := fx.New(
		fx.NopLogger,
		fx.Provide(newUnusedA),
		fx.Decorate(func(a *unusedTypeA) *unusedTypeA { return a }),
		fx.Invoke(func(*unusedTypeA) {}),
	)
	require.NoError(t, app.Err())
	report := app.UnusedDependencies()
	assert.True(t, report.Complete)
	assert.Empty(t, report.Uncalled)
	assert.Empty(t, report.Unconsumed)
}

// Flattened value group members are reported by their element type and get
// consumed when the (hard) group is requested.
func TestUnusedDependencies_FlattenGroupConsumed(t *testing.T) {
	type param struct {
		fx.In

		Values []string `group:"flat"`
	}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			fx.Annotated{
				Group:  "flat,flatten",
				Target: func() []string { return []string{"x", "y"} },
			},
		),
		fx.Invoke(func(p param) {
			assert.ElementsMatch(t, []string{"x", "y"}, p.Values)
		}),
	)
	require.NoError(t, app.Err())
	report := app.UnusedDependencies()
	assert.True(t, report.Complete)
	assert.Empty(t, report.Uncalled)
	assert.Empty(t, report.Unconsumed)
}
