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
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Struct tags that may be placed on a field of a parameter object (a struct
// embedding [In]) which consumes a value group through a `group:".."` tag.
//
// The "min" tag requires the receiver of the group to get at least the given
// number of members, and the "max" tag limits the number of members the
// receiver may get. The count is checked against the slice the receiver
// actually receives, so flattened slices (see the ",flatten" group option)
// and value-group decorators are taken into account.
//
//	type Params struct {
//	  fx.In
//
//	  Plugins []Plugin `group:"plugins" min:"1" max:"10"`
//	}
//
// If the constraint is not satisfied, the receiver is not called and the
// application fails to start with an error naming the group, the expected
// count range, and the actual count.
//
// Value groups without a "min" or "max" tag continue to be allowed to be
// empty.
const (
	_groupMinTag = "min"
	_groupMaxTag = "max"
)

// unsetBound marks a "min" or "max" bound that was not declared.
const unsetBound = -1

// errGroupMemberCount is returned before a receiver is called when a value
// group it consumes does not satisfy its declared member count constraints.
type errGroupMemberCount struct {
	// Group is the name of the value group, as specified in its
	// `group:".."` tag.
	Group string

	// Min is the inclusive lower bound on the number of group members,
	// or unsetBound if no lower bound was declared.
	Min int

	// Max is the inclusive upper bound on the number of group members,
	// or unsetBound if no upper bound was declared.
	Max int

	// Count is the number of group members the receiver actually got.
	Count int
}

func (e *errGroupMemberCount) Error() string {
	var wants string
	switch {
	case e.Min != unsetBound && e.Max != unsetBound:
		wants = fmt.Sprintf("between %d and %d members", e.Min, e.Max)
	case e.Min != unsetBound:
		wants = fmt.Sprintf("at least %s", pluralize("member", e.Min))
	default:
		wants = fmt.Sprintf("at most %s", pluralize("member", e.Max))
	}
	return fmt.Sprintf(
		"value group %q expects %s, got %d",
		e.Group, wants, e.Count,
	)
}

func pluralize(word string, n int) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// groupMemberConstraint describes a member count constraint on a single
// value group consumed by a receiver function.
type groupMemberConstraint struct {
	// argIndex is the index of the parameter in the receiver's
	// signature that holds the parameter object.
	argIndex int

	// fieldPath walks from the parameter object to the grouped slice
	// field, supporting nested parameter objects.
	fieldPath []int

	// group is the name of the value group.
	group string

	// minCount and maxCount are the inclusive bounds; unsetBound means
	// the bound was not declared.
	minCount int
	maxCount int
}

// extractGroupMemberConstraints walks the parameters of a receiver function
// and returns the value-group member count constraints declared through
// "min"/"max" struct tags.
//
// Only parameter objects (structs embedding [In]) are inspected, since a
// value group can only be consumed through a `group:".."` field tag.
func extractGroupMemberConstraints(ft reflect.Type) ([]groupMemberConstraint, error) {
	var constraints []groupMemberConstraint

	numIn := ft.NumIn()
	if ft.IsVariadic() {
		// Like dig, variadic arguments are not filled as dependencies.
		numIn--
	}

	for argIdx := 0; argIdx < numIn; argIdx++ {
		paramType := ft.In(argIdx)
		if !isIn(paramType) {
			continue
		}

		cs, err := extractInStructConstraints(paramType, argIdx, nil)
		if err != nil {
			return nil, err
		}
		constraints = append(constraints, cs...)
	}

	return constraints, nil
}

// extractInStructConstraints collects constraints from the fields of a
// parameter object, recursing into nested parameter objects.
func extractInStructConstraints(
	t reflect.Type,
	argIdx int,
	path []int,
) ([]groupMemberConstraint, error) {
	var constraints []groupMemberConstraint

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Type == reflect.TypeOf(In{}) {
			// Skip the embedded fx.In marker.
			continue
		}
		if f.PkgPath != "" {
			// Unexported fields are rejected by dig itself; ignore them
			// here so that dig produces its own error.
			continue
		}

		fieldPath := append(append([]int(nil), path...), i)

		if isIn(f.Type) {
			cs, err := extractInStructConstraints(f.Type, argIdx, fieldPath)
			if err != nil {
				return nil, err
			}
			constraints = append(constraints, cs...)
			continue
		}

		minTag := f.Tag.Get(_groupMinTag)
		maxTag := f.Tag.Get(_groupMaxTag)
		if minTag == "" && maxTag == "" {
			continue
		}

		where := fmt.Sprintf("field %q of %v", f.Name, t)
		groupTag := f.Tag.Get(_groupTag)
		switch {
		case groupTag == "":
			return nil, fmt.Errorf(
				"%q and %q tags may only be used on value group fields: %s has no %q tag",
				_groupMinTag, _groupMaxTag, where, _groupTag)
		case f.Type.Kind() != reflect.Slice:
			return nil, fmt.Errorf(
				"%q and %q tags may only be used on value group slice fields: %s is %v",
				_groupMinTag, _groupMaxTag, where, f.Type)
		}

		minCount, err := parseBound(_groupMinTag, minTag, where)
		if err != nil {
			return nil, err
		}
		maxCount, err := parseBound(_groupMaxTag, maxTag, where)
		if err != nil {
			return nil, err
		}
		if minCount != unsetBound && maxCount != unsetBound && minCount > maxCount {
			return nil, fmt.Errorf(
				"%s has %s:%d greater than %s:%d",
				where, _groupMinTag, minCount, _groupMaxTag, maxCount)
		}

		groupName := strings.SplitN(groupTag, ",", 2)[0]
		constraints = append(constraints, groupMemberConstraint{
			argIndex:  argIdx,
			fieldPath: fieldPath,
			group:     groupName,
			minCount:  minCount,
			maxCount:  maxCount,
		})
	}

	return constraints, nil
}

// parseBound parses a "min"/"max" tag value into a non-negative integer.
func parseBound(tag, value, where string) (int, error) {
	if value == "" {
		return unsetBound, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return unsetBound, fmt.Errorf(
			"%s has invalid %q tag: expected a non-negative integer, got %q",
			where, tag, value)
	}
	return n, nil
}

// wrapWithGroupConstraints wraps a receiver function so that the member
// count constraints declared on its value group parameters are checked
// against the members it actually receives, before the receiver is called.
//
// The returned function has the same parameters and results as the input,
// plus an error result if the input did not already return one. If the
// input declares no constraints (or is not a function), it is returned
// unchanged.
func wrapWithGroupConstraints(fn any) (any, error) {
	ft := reflect.TypeOf(fn)
	if ft == nil || ft.Kind() != reflect.Func {
		return fn, nil
	}

	constraints, err := extractGroupMemberConstraints(ft)
	if err != nil {
		return nil, err
	}
	if len(constraints) == 0 {
		return fn, nil
	}

	origFn := reflect.ValueOf(fn)

	inTypes := make([]reflect.Type, ft.NumIn())
	for i := range inTypes {
		inTypes[i] = ft.In(i)
	}

	outTypes := make([]reflect.Type, 0, ft.NumOut()+1)
	hasError := ft.NumOut() > 0 && ft.Out(ft.NumOut()-1) == _typeOfError
	for i := 0; i < ft.NumOut(); i++ {
		outTypes = append(outTypes, ft.Out(i))
	}
	if !hasError {
		outTypes = append(outTypes, _typeOfError)
	}

	wrappedType := reflect.FuncOf(inTypes, outTypes, ft.IsVariadic())
	wrapped := reflect.MakeFunc(wrappedType, func(args []reflect.Value) []reflect.Value {
		for _, c := range constraints {
			v := args[c.argIndex]
			for _, idx := range c.fieldPath {
				v = v.Field(idx)
			}
			count := v.Len()
			if (c.minCount != unsetBound && count < c.minCount) ||
				(c.maxCount != unsetBound && count > c.maxCount) {
				return failedGroupConstraintResults(outTypes, &errGroupMemberCount{
					Group: c.group,
					Min:   c.minCount,
					Max:   c.maxCount,
					Count: count,
				})
			}
		}

		results := origFn.Call(args)
		if !hasError {
			results = append(results, _nilError)
		}
		return results
	})

	return wrapped.Interface(), nil
}

// failedGroupConstraintResults builds zero-valued results for a wrapped
// receiver, with the last (error) result set to the constraint error, so
// that the receiver itself is never called.
func failedGroupConstraintResults(
	outTypes []reflect.Type,
	err error,
) []reflect.Value {
	results := make([]reflect.Value, len(outTypes))
	for i, t := range outTypes {
		results[i] = reflect.Zero(t)
	}
	results[len(results)-1] = reflect.ValueOf(err)
	return results
}
