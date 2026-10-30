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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
)

// groupConstraintPlugin is the value type used by the value groups in the
// tests below.
type groupConstraintPlugin struct {
	name string
}

// Parameter objects with various member count constraints.
type gcMin1Params struct {
	fx.In

	Plugins []groupConstraintPlugin `group:"plugins" min:"1"`
}

type gcMin2Params struct {
	fx.In

	Plugins []groupConstraintPlugin `group:"plugins" min:"2"`
}

type gcMax1Params struct {
	fx.In

	Plugins []groupConstraintPlugin `group:"plugins" max:"1"`
}

type gcMax2Params struct {
	fx.In

	Plugins []groupConstraintPlugin `group:"plugins" max:"2"`
}

type gcRangeParams struct {
	fx.In

	Plugins []groupConstraintPlugin `group:"plugins" min:"2" max:"3"`
}

type gcNoConstraintParams struct {
	fx.In

	Plugins []groupConstraintPlugin `group:"plugins"`
}

// gcPluginProvider returns an option that provides count distinct members
// to the "plugins" group.
func gcPluginProviders(count int) []fx.Option {
	opts := make([]fx.Option, 0, count)
	for i := 0; i < count; i++ {
		i := i
		opts = append(opts, fx.Provide(fx.Annotated{
			Group: "plugins",
			Target: func() groupConstraintPlugin {
				return groupConstraintPlugin{name: fmt.Sprintf("plugin-%d", i)}
			},
		}))
	}
	return opts
}

// gcFlattenProvider returns an option that provides count members to the
// "plugins" group through a single, flattened slice.
func gcFlattenProvider(count int) fx.Option {
	return fx.Provide(fx.Annotated{
		Group: "plugins,flatten",
		Target: func() []groupConstraintPlugin {
			plugins := make([]groupConstraintPlugin, count)
			for i := range plugins {
				plugins[i] = groupConstraintPlugin{name: fmt.Sprintf("plugin-%d", i)}
			}
			return plugins
		},
	})
}

func TestGroupMemberConstraintBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		params     any // parameter type of the receiver
		providers  int
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:       "min bound fails on empty group",
			params:     gcMin1Params{},
			providers:  0,
			wantErr:    true,
			wantErrMsg: `value group "plugins" expects at least 1 member, got 0`,
		},
		{
			name:      "min bound satisfied",
			params:    gcMin1Params{},
			providers: 1,
		},
		{
			name:       "min bound fails below lower bound",
			params:     gcMin2Params{},
			providers:  1,
			wantErr:    true,
			wantErrMsg: `value group "plugins" expects at least 2 members, got 1`,
		},
		{
			name:      "min bound satisfied at lower bound",
			params:    gcMin2Params{},
			providers: 2,
		},
		{
			name:       "max bound fails above upper bound",
			params:     gcMax1Params{},
			providers:  2,
			wantErr:    true,
			wantErrMsg: `value group "plugins" expects at most 1 member, got 2`,
		},
		{
			name:      "max bound satisfied at upper bound",
			params:    gcMax2Params{},
			providers: 2,
		},
		{
			name:       "range fails below lower bound",
			params:     gcRangeParams{},
			providers:  1,
			wantErr:    true,
			wantErrMsg: `value group "plugins" expects between 2 and 3 members, got 1`,
		},
		{
			name:       "range fails above upper bound",
			params:     gcRangeParams{},
			providers:  4,
			wantErr:    true,
			wantErrMsg: `value group "plugins" expects between 2 and 3 members, got 4`,
		},
		{
			name:      "range satisfied",
			params:    gcRangeParams{},
			providers: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			called := false
			opts := append(gcPluginProviders(tt.providers),
				fx.NopLogger,
				gcInvokeWithParams(t, tt.params, &called),
			)

			app := fx.New(opts...)
			if tt.wantErr {
				err := app.Err()
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrMsg)
				// The receiver must not have been called when the
				// constraint is not satisfied.
				assert.False(t, called, "receiver must not be called on constraint failure")
				return
			}
			require.NoError(t, app.Err())
			assert.True(t, called, "receiver must be called when the constraint is satisfied")
		})
	}
}

// gcInvokeWithParams builds an fx.Invoke option whose function takes the
// concrete parameter object type of the given value, marking called when the
// receiver actually runs.
func gcInvokeWithParams(t *testing.T, params any, called *bool) fx.Option {
	t.Helper()

	var fn any
	switch params.(type) {
	case gcMin1Params:
		fn = func(p gcMin1Params) {
			*called = true
		}
	case gcMin2Params:
		fn = func(p gcMin2Params) {
			*called = true
		}
	case gcMax1Params:
		fn = func(p gcMax1Params) {
			*called = true
		}
	case gcMax2Params:
		fn = func(p gcMax2Params) {
			*called = true
		}
	case gcRangeParams:
		fn = func(p gcRangeParams) {
			*called = true
		}
	case gcNoConstraintParams:
		fn = func(p gcNoConstraintParams) {
			*called = true
		}
	default:
		t.Fatalf("unsupported parameter type %T", params)
	}
	return fx.Invoke(fn)
}

func TestGroupMemberConstraintEmptyGroupAllowedWithoutTag(t *testing.T) {
	t.Parallel()

	called := false
	app := fx.New(
		fx.NopLogger,
		fx.Invoke(func(p gcNoConstraintParams) {
			called = true
			assert.Empty(t, p.Plugins)
		}),
	)
	require.NoError(t, app.Err())
	assert.True(t, called)
}

func TestGroupMemberConstraintConstructorReceiver(t *testing.T) {
	t.Parallel()

	t.Run("constructor not called when min bound fails", func(t *testing.T) {
		t.Parallel()

		ctorCalled := false
		type manager struct{}

		app := fx.New(
			fx.NopLogger,
			fx.Provide(func(p gcMin1Params) *manager {
				ctorCalled = true
				return &manager{}
			}),
			fx.Invoke(func(*manager) {}),
		)
		err := app.Err()
		require.Error(t, err)
		assert.Contains(t, err.Error(),
			`value group "plugins" expects at least 1 member, got 0`)
		assert.False(t, ctorCalled,
			"constructor receiver must not be called on constraint failure")
	})

	t.Run("constructor returning error not called when max bound fails", func(t *testing.T) {
		t.Parallel()

		ctorCalled := false
		type manager struct{}

		opts := append(gcPluginProviders(2),
			fx.NopLogger,
			fx.Provide(func(p gcMax1Params) (*manager, error) {
				ctorCalled = true
				return &manager{}, nil
			}),
			fx.Invoke(func(*manager) {}),
		)
		app := fx.New(opts...)
		err := app.Err()
		require.Error(t, err)
		assert.Contains(t, err.Error(),
			`value group "plugins" expects at most 1 member, got 2`)
		assert.False(t, ctorCalled)
	})

	t.Run("constructor called when constraint is satisfied", func(t *testing.T) {
		t.Parallel()

		ctorCalled := false
		type manager struct{}

		opts := append(gcPluginProviders(1),
			fx.NopLogger,
			fx.Provide(func(p gcMin1Params) *manager {
				ctorCalled = true
				assert.Len(t, p.Plugins, 1)
				return &manager{}
			}),
			fx.Invoke(func(*manager) {}),
		)
		app := fx.New(opts...)
		require.NoError(t, app.Err())
		assert.True(t, ctorCalled)
	})
}

func TestGroupMemberConstraintFlatten(t *testing.T) {
	t.Parallel()

	t.Run("flattened slice members are counted individually", func(t *testing.T) {
		t.Parallel()

		called := false
		app := fx.New(
			fx.NopLogger,
			gcFlattenProvider(3),
			fx.Invoke(func(p gcMin2Params) {
				called = true
				assert.Len(t, p.Plugins, 3)
			}),
		)
		require.NoError(t, app.Err())
		assert.True(t, called)
	})

	t.Run("min bound fails against flattened member count", func(t *testing.T) {
		t.Parallel()

		called := false
		app := fx.New(
			fx.NopLogger,
			gcFlattenProvider(3),
			fx.Invoke(func(p struct {
				fx.In

				Plugins []groupConstraintPlugin `group:"plugins" min:"4"`
			},
			) {
				called = true
			}),
		)
		err := app.Err()
		require.Error(t, err)
		assert.Contains(t, err.Error(),
			`value group "plugins" expects at least 4 members, got 3`)
		assert.False(t, called)
	})

	t.Run("flattened members count alongside regular providers", func(t *testing.T) {
		t.Parallel()

		called := false
		opts := append(gcPluginProviders(1),
			fx.NopLogger,
			gcFlattenProvider(3),
			fx.Invoke(func(p struct {
				fx.In

				Plugins []groupConstraintPlugin `group:"plugins" min:"4" max:"4"`
			},
			) {
				called = true
				assert.Len(t, p.Plugins, 4)
			}),
		)
		app := fx.New(opts...)
		require.NoError(t, app.Err())
		assert.True(t, called)
	})
}

func TestGroupMemberConstraintDecoratedGroup(t *testing.T) {
	t.Parallel()

	// Decorator parameter and result objects for the "plugins" group.
	type decoratorIn struct {
		fx.In

		Plugins []groupConstraintPlugin `group:"plugins"`
	}
	type decoratorOut struct {
		fx.Out

		Plugins []groupConstraintPlugin `group:"plugins"`
	}

	t.Run("count reflects group decoration that removes members", func(t *testing.T) {
		t.Parallel()

		called := false
		opts := append(gcPluginProviders(2),
			fx.NopLogger,
			fx.Decorate(func(p decoratorIn) decoratorOut {
				// Keep only the first member.
				return decoratorOut{Plugins: p.Plugins[:1]}
			}),
			fx.Invoke(func(p gcMin2Params) {
				called = true
			}),
		)
		app := fx.New(opts...)
		err := app.Err()
		require.Error(t, err)
		assert.Contains(t, err.Error(),
			`value group "plugins" expects at least 2 members, got 1`)
		assert.False(t, called)
	})

	t.Run("count reflects group decoration that drops the group to zero", func(t *testing.T) {
		t.Parallel()

		called := false
		opts := append(gcPluginProviders(2),
			fx.NopLogger,
			fx.Decorate(func(p decoratorIn) decoratorOut {
				return decoratorOut{Plugins: nil}
			}),
			fx.Invoke(func(p gcMin1Params) {
				called = true
			}),
		)
		app := fx.New(opts...)
		err := app.Err()
		require.Error(t, err)
		assert.Contains(t, err.Error(),
			`value group "plugins" expects at least 1 member, got 0`)
		assert.False(t, called)
	})

	t.Run("count reflects group decoration that adds members", func(t *testing.T) {
		t.Parallel()

		called := false
		opts := append(gcPluginProviders(1),
			fx.NopLogger,
			fx.Decorate(func(p decoratorIn) decoratorOut {
				return decoratorOut{
					Plugins: append(p.Plugins,
						groupConstraintPlugin{name: "decorated-1"},
						groupConstraintPlugin{name: "decorated-2"},
					),
				}
			}),
			fx.Invoke(func(p gcMin2Params) {
				called = true
				assert.Len(t, p.Plugins, 3)
			}),
		)
		app := fx.New(opts...)
		require.NoError(t, app.Err())
		assert.True(t, called)
	})
}

func TestGroupMemberConstraintModuleVisibility(t *testing.T) {
	t.Parallel()

	t.Run("private members of a sibling module are not counted", func(t *testing.T) {
		t.Parallel()

		called := false
		app := fx.New(
			fx.NopLogger,
			fx.Module("providers",
				fx.Provide(
					fx.Annotated{
						Group: "plugins",
						Target: func() groupConstraintPlugin {
							return groupConstraintPlugin{name: "private"}
						},
					},
					fx.Private,
				),
			),
			fx.Module("consumers",
				fx.Invoke(func(p gcMin1Params) {
					called = true
				}),
			),
		)
		err := app.Err()
		require.Error(t, err)
		assert.Contains(t, err.Error(),
			`value group "plugins" expects at least 1 member, got 0`)
		assert.False(t, called)
	})

	t.Run("private members are visible inside their own module", func(t *testing.T) {
		t.Parallel()

		called := false
		app := fx.New(
			fx.NopLogger,
			fx.Module("mymodule",
				fx.Provide(
					fx.Annotated{
						Group: "plugins",
						Target: func() groupConstraintPlugin {
							return groupConstraintPlugin{name: "private"}
						},
					},
					fx.Private,
				),
				fx.Invoke(func(p gcMin1Params) {
					called = true
					assert.Len(t, p.Plugins, 1)
				}),
			),
		)
		require.NoError(t, app.Err())
		assert.True(t, called)
	})

	t.Run("exported child members are visible to the parent", func(t *testing.T) {
		t.Parallel()

		called := false
		app := fx.New(
			fx.NopLogger,
			fx.Module("child",
				fx.Provide(fx.Annotated{
					Group: "plugins",
					Target: func() groupConstraintPlugin {
						return groupConstraintPlugin{name: "exported"}
					},
				}),
			),
			fx.Invoke(func(p gcMin1Params) {
				called = true
				assert.Len(t, p.Plugins, 1)
			}),
		)
		require.NoError(t, app.Err())
		assert.True(t, called)
	})

	t.Run("nested receiver only counts members visible to its scope", func(t *testing.T) {
		t.Parallel()

		childCalled := false
		parentCalled := false
		app := fx.New(
			fx.NopLogger,
			// One member exported to the root scope.
			fx.Provide(fx.Annotated{
				Group: "plugins",
				Target: func() groupConstraintPlugin {
					return groupConstraintPlugin{name: "root"}
				},
			}),
			fx.Module("child",
				// One more private member visible only inside child.
				fx.Provide(
					fx.Annotated{
						Group: "plugins",
						Target: func() groupConstraintPlugin {
							return groupConstraintPlugin{name: "child-private"}
						},
					},
					fx.Private,
				),
				// Child sees both members.
				fx.Invoke(func(p gcMin2Params) {
					childCalled = true
					assert.Len(t, p.Plugins, 2)
				}),
			),
			// Parent only sees the exported member.
			fx.Invoke(func(p gcMin2Params) {
				parentCalled = true
			}),
		)
		err := app.Err()
		require.Error(t, err)
		assert.Contains(t, err.Error(),
			`value group "plugins" expects at least 2 members, got 1`)
		assert.True(t, childCalled,
			"child receiver must run and see the private member")
		assert.False(t, parentCalled,
			"parent receiver must not run: it only sees one member")
	})
}

func TestGroupMemberConstraintNestedParameterObject(t *testing.T) {
	t.Parallel()

	type GCInnerParams struct {
		fx.In

		Plugins []groupConstraintPlugin `group:"plugins" min:"1"`
	}
	type nestedParams struct {
		fx.In

		GCInnerParams
	}

	t.Run("constraint in nested In struct fails", func(t *testing.T) {
		t.Parallel()

		called := false
		app := fx.New(
			fx.NopLogger,
			fx.Invoke(func(p nestedParams) {
				called = true
			}),
		)
		err := app.Err()
		require.Error(t, err)
		assert.Contains(t, err.Error(),
			`value group "plugins" expects at least 1 member, got 0`)
		assert.False(t, called)
	})

	t.Run("constraint in nested In struct passes", func(t *testing.T) {
		t.Parallel()

		called := false
		opts := append(gcPluginProviders(1),
			fx.NopLogger,
			fx.Invoke(func(p nestedParams) {
				called = true
				assert.Len(t, p.Plugins, 1)
			}),
		)
		app := fx.New(opts...)
		require.NoError(t, app.Err())
		assert.True(t, called)
	})
}

func TestGroupMemberConstraintIndependentGroups(t *testing.T) {
	t.Parallel()

	type twoGroupsParams struct {
		fx.In

		Primary   []groupConstraintPlugin `group:"primary" min:"1"`
		Secondary []groupConstraintPlugin `group:"secondary"`
	}

	called := false
	app := fx.New(
		fx.NopLogger,
		fx.Provide(fx.Annotated{
			Group:  "primary",
			Target: func() groupConstraintPlugin { return groupConstraintPlugin{name: "p"} },
		}),
		// "secondary" is intentionally empty and declares no constraint.
		fx.Invoke(func(p twoGroupsParams) {
			called = true
			assert.Len(t, p.Primary, 1)
			assert.Empty(t, p.Secondary)
		}),
	)
	require.NoError(t, app.Err())
	assert.True(t, called)
}

func TestGroupMemberConstraintInvalidTags(t *testing.T) {
	t.Parallel()

	t.Run("min must be an integer", func(t *testing.T) {
		t.Parallel()

		type badParams struct {
			fx.In

			Plugins []groupConstraintPlugin `group:"plugins" min:"one"`
		}

		app := fx.New(
			fx.NopLogger,
			fx.Provide(func() groupConstraintPlugin {
				return groupConstraintPlugin{name: "p"}
			}),
			fx.Invoke(func(badParams) {}),
		)
		err := app.Err()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `invalid "min" tag`)
	})

	t.Run("min cannot be negative", func(t *testing.T) {
		t.Parallel()

		type badParams struct {
			fx.In

			Plugins []groupConstraintPlugin `group:"plugins" min:"-1"`
		}

		app := fx.New(
			fx.NopLogger,
			fx.Invoke(func(badParams) {}),
		)
		err := app.Err()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `invalid "min" tag`)
	})

	t.Run("min cannot be greater than max", func(t *testing.T) {
		t.Parallel()

		type badParams struct {
			fx.In

			Plugins []groupConstraintPlugin `group:"plugins" min:"5" max:"2"`
		}

		app := fx.New(
			fx.NopLogger,
			fx.Invoke(func(badParams) {}),
		)
		err := app.Err()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `min:5 greater than max:2`)
	})

	t.Run("bounds require a group tag", func(t *testing.T) {
		t.Parallel()

		type badParams struct {
			fx.In

			Plugins []groupConstraintPlugin `min:"1"`
		}

		app := fx.New(
			fx.NopLogger,
			fx.Invoke(func(badParams) {}),
		)
		err := app.Err()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "may only be used on value group fields")
	})

	t.Run("bounds require a slice field", func(t *testing.T) {
		t.Parallel()

		type badParams struct {
			fx.In

			Plugin groupConstraintPlugin `group:"plugins" min:"1"`
		}

		app := fx.New(
			fx.NopLogger,
			fx.Invoke(func(badParams) {}),
		)
		err := app.Err()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "may only be used on value group slice fields")
	})

	t.Run("invalid tags on a provided constructor fail registration", func(t *testing.T) {
		t.Parallel()

		type badParams struct {
			fx.In

			Plugins []groupConstraintPlugin `group:"plugins" max:"many"`
		}
		type manager struct{}

		app := fx.New(
			fx.NopLogger,
			fx.Provide(func(p badParams) *manager { return &manager{} }),
		)
		err := app.Err()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `invalid "max" tag`)
	})
}
