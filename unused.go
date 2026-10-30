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

package fx

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"go.uber.org/dig"
)

// UnusedDependencyReport describes constructors that were registered with
// [Provide] or [Supply] but that the application did not need after it
// finished assembling.
//
// The report is computed purely from information Fx already observes while
// building the dependency graph: requesting it never calls a constructor and
// never starts any lifecycle hooks.
//
// Constructors fall into one of two categories:
//
//   - [UnusedDependencyReport.Uncalled]: constructors that Dig never invoked.
//     None of the values they produce were required to satisfy an [Invoke].
//   - [UnusedDependencyReport.Unconsumed]: constructors that Dig invoked
//     (because at least one of their outputs was required) but which also
//     produced additional outputs that nothing consumed. This is only
//     possible for constructors that return multiple values or use [Out]
//     result objects.
//
// When the application failed to assemble ([App.Err] is non-nil), or it was
// built in validate (dry-run) mode, [UnusedDependencyReport.Complete] is
// false and both lists are empty. In that case the report only covers the
// phases that ran, and constructors belonging to phases that never executed
// are not reported as unused because they might have been required had the
// rest of the application run. See [UnusedDependencyReport.IncompleteReason]
// for details.
type UnusedDependencyReport struct {
	// Complete reports whether the whole application finished assembling.
	//
	// It is false if any Provide/Decorate registration failed, an Invoke
	// failed, or the application was built in validate (dry-run) mode. When
	// false, the unused lists are left empty: a constructor that did not run
	// may simply belong to a phase that never executed, so it cannot be
	// declared unused.
	Complete bool

	// IncompleteReason explains why the report is not complete.
	// It is empty when Complete is true.
	IncompleteReason string

	// Uncalled lists constructors that were registered but never invoked.
	Uncalled []UnusedProvider

	// Unconsumed lists constructors that were invoked but which produced one
	// or more outputs that no constructor, decorator, or invocation consumed.
	Unconsumed []UnusedProvider
}

// UnusedProvider describes a constructor whose outputs were not fully
// consumed by the assembled application.
type UnusedProvider struct {
	// Name is the name of the constructor.
	//
	// For values supplied with [Supply], this is the supplied type prefixed
	// with fx.Supply.
	Name string

	// Module is the name of the module the constructor was registered in.
	// It is empty for constructors registered at the top level of the
	// application.
	Module string

	// Private reports whether the constructor was registered with [Private].
	Private bool

	// Outputs are the constructor outputs that were not needed.
	Outputs []UnusedOutput
}

// UnusedOutput describes a single value produced by a constructor that was
// not consumed.
type UnusedOutput struct {
	// Type is the type of the value, rendered the same way Dig renders it in
	// graphs and log events.
	Type string

	// Name is the name tag of the value, if it was a named value.
	Name string

	// Group is the value group the value belongs to, if it was a group
	// member.
	Group string

	// Soft is true when this value is a member of a soft value group that
	// was consumed with the `soft` group option.
	//
	// A soft value group only includes values produced by constructors that
	// were already called to satisfy some other (non-soft) dependency.
	// Consuming a soft group therefore never pulls a constructor in on its
	// own, which is why this constructor was not called even though the
	// group was consumed.
	Soft bool

	// Note explains, in human-readable terms, why the output is considered
	// unused. It is empty for outputs that simply have no consumer.
	Note string
}

// String renders the report as human-readable text.
func (r UnusedDependencyReport) String() string {
	var b strings.Builder
	if !r.Complete {
		b.WriteString("unused dependencies report is incomplete: ")
		b.WriteString(r.IncompleteReason)
		return b.String()
	}

	if len(r.Uncalled) == 0 && len(r.Unconsumed) == 0 {
		return "no unused dependencies: every registered constructor was needed"
	}

	if len(r.Uncalled) > 0 {
		b.WriteString("constructors registered but never called:\n")
		writeUnusedProviders(&b, r.Uncalled)
	}
	if len(r.Unconsumed) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("constructors called with unconsumed outputs:\n")
		writeUnusedProviders(&b, r.Unconsumed)
	}
	return strings.TrimRight(b.String(), "\n")
}

func writeUnusedProviders(b *strings.Builder, providers []UnusedProvider) {
	for _, p := range providers {
		fmt.Fprintf(b, "  - %s", p.Name)
		switch {
		case p.Module != "":
			fmt.Fprintf(b, " (module: %q", p.Module)
		default:
			b.WriteString(" (module: \"\"")
		}
		if p.Private {
			b.WriteString(", private")
		}
		b.WriteString(")\n")

		for _, o := range p.Outputs {
			fmt.Fprintf(b, "      %s\n", describeUnusedOutput(o))
		}
	}
}

func describeUnusedOutput(o UnusedOutput) string {
	switch {
	case o.Group != "":
		s := fmt.Sprintf("unconsumed group member: type %s, group %q", o.Type, o.Group)
		if o.Note != "" {
			s += " (" + o.Note + ")"
		}
		return s
	case o.Name != "":
		s := fmt.Sprintf("unconsumed named value: type %s, name %q", o.Type, o.Name)
		if o.Note != "" {
			s += " (" + o.Note + ")"
		}
		return s
	default:
		s := fmt.Sprintf("unconsumed value: type %s", o.Type)
		if o.Note != "" {
			s += " (" + o.Note + ")"
		}
		return s
	}
}

// UnusedDependencies returns a report of the constructors that were provided
// to the application but that were not needed after it finished assembling.
//
// This method never invokes a constructor, populates a value, or runs a
// lifecycle hook: it only inspects information collected while the
// application was being built. It is safe to call at any time, but the
// report is only complete (see [UnusedDependencyReport.Complete]) once
// application assembly has finished without errors, i.e. once [App.Err] is
// nil.
func (app *App) UnusedDependencies() UnusedDependencyReport {
	return app.tracker.build(app)
}

// depKind identifies how a value is addressed in the graph.
type depKind int

const (
	depValue depKind = iota
	depGroup
)

// depKey identifies a graph value independently of how Dig renders it.
// For group values, typ is the element type, not the slice type.
type depKey struct {
	kind depKind
	typ  string
	tag  string // name for values, group name for groups
}

// graphNode is anything that can produce outputs and/or consume inputs:
// user providers, decorators, and Fx's own implicit providers (such as the
// custom event logger constructor).
type graphNode struct {
	name       string
	mod        *module
	private    bool
	isSupply   bool
	reportable bool // included in the report (user providers)

	inputs  []depInput
	outputs []depOutput

	called bool
}

type depOutput struct {
	// display is Dig's canonical rendering of the output.
	display string
	key     depKey
}

type depInput struct {
	key depKey
	// soft is true for group parameters tagged `group:"...,soft"`.
	soft bool
}

type invokeNode struct {
	mod    *module
	inputs []depInput
	ran    bool
}

// dependencyTracker observes graph construction without influencing it. It
// records what was registered and what Dig actually invoked. All recording
// happens through Dig's provide/decorator/invoke callbacks that Fx already
// installs; the tracker itself never resolves a value.
type dependencyTracker struct {
	mu sync.Mutex

	// nodes holds providers and decorators (and Fx's implicit providers),
	// in registration order. Only nodes with reportable=true appear in the
	// report.
	nodes []*graphNode

	// invokes holds the registered invocations in execution order.
	invokes []*invokeNode
}

func newDependencyTracker() *dependencyTracker {
	return &dependencyTracker{}
}

// provider registers a user-provided constructor and returns the node that
// callbacks should mark as called. It returns nil for Fx's built-in
// providers, which must never appear in the report.
func (t *dependencyTracker) provider(name string, mod *module, private, isSupply, builtin bool) *graphNode {
	if builtin {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	n := &graphNode{
		name:       name,
		mod:        mod,
		private:    private,
		isSupply:   isSupply,
		reportable: true,
	}
	t.nodes = append(t.nodes, n)
	return n
}

// decorator registers a decorator, which only consumes values. Decorators
// are never reported as unused providers.
func (t *dependencyTracker) decorator(mod *module) *graphNode {
	t.mu.Lock()
	defer t.mu.Unlock()

	n := &graphNode{name: "decorator", mod: mod}
	t.nodes = append(t.nodes, n)
	return n
}

// implicitProvider registers a provider Fx creates itself (for example the
// custom logger constructor). It is never reported, but the values it
// consumes count as consumed once it ran.
func (t *dependencyTracker) implicitProvider(mod *module) *graphNode {
	t.mu.Lock()
	defer t.mu.Unlock()

	n := &graphNode{name: "fx-implicit-provider", mod: mod}
	t.nodes = append(t.nodes, n)
	return n
}

func (t *dependencyTracker) invoke(mod *module) *invokeNode {
	t.mu.Lock()
	defer t.mu.Unlock()

	inv := &invokeNode{mod: mod}
	t.invokes = append(t.invokes, inv)
	return inv
}

func (t *dependencyTracker) markCalled(n *graphNode) {
	if n == nil {
		return
	}
	t.mu.Lock()
	n.called = true
	t.mu.Unlock()
}

// setOutputs records the static outputs of a provider.
func (t *dependencyTracker) setOutputs(n *graphNode, outputs []*dig.Output) {
	if n == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	n.outputs = make([]depOutput, 0, len(outputs))
	for _, o := range outputs {
		n.outputs = append(n.outputs, parseDepOutput(o.String()))
	}
}

// setInputs records the static inputs of a provider or decorator. fn is the
// actual function handed to Dig (after Fx annotations are applied) and is
// used only to detect soft value group parameters, which Dig does not expose
// through ProvideInfo/DecorateInfo.
func (t *dependencyTracker) setInputs(n *graphNode, inputs []*dig.Input, fn any) {
	if n == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	soft := softGroupKeys(fn)
	n.inputs = make([]depInput, 0, len(inputs))
	for _, in := range inputs {
		d := parseDepInput(in.String())
		if d.key.kind == depGroup {
			d.soft = soft[d.key]
		}
		n.inputs = append(n.inputs, d)
	}
}

func (t *dependencyTracker) setInvokeInputs(inv *invokeNode, inputs []*dig.Input, fn any) {
	if inv == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	soft := softGroupKeys(fn)
	inv.inputs = make([]depInput, 0, len(inputs))
	for _, in := range inputs {
		d := parseDepInput(in.String())
		if d.key.kind == depGroup {
			d.soft = soft[d.key]
		}
		inv.inputs = append(inv.inputs, d)
	}
}

func (t *dependencyTracker) markInvoked(inv *invokeNode) {
	if inv == nil {
		return
	}
	t.mu.Lock()
	inv.ran = true
	t.mu.Unlock()
}

type groupConsumer struct {
	mod  *module
	soft bool
}

func (t *dependencyTracker) build(app *App) UnusedDependencyReport {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.isComplete(app) {
		return UnusedDependencyReport{
			Complete:         false,
			IncompleteReason: t.incompleteReason(app),
		}
	}

	// Collect every value requested by a constructor/decorator that ran and
	// by every invocation that ran.
	singleConsumers := make(map[depKey][]*module)
	groupConsumers := make(map[depKey][]groupConsumer)

	consume := func(inputs []depInput, mod *module) {
		for _, in := range inputs {
			switch in.key.kind {
			case depValue:
				singleConsumers[in.key] = append(singleConsumers[in.key], mod)
			case depGroup:
				groupConsumers[in.key] = append(groupConsumers[in.key], groupConsumer{
					mod:  mod,
					soft: in.soft,
				})
			}
		}
	}

	for _, n := range t.nodes {
		if n.called {
			consume(n.inputs, n.mod)
		}
	}
	for _, inv := range t.invokes {
		if inv.ran {
			consume(inv.inputs, inv.mod)
		}
	}

	// hasSingleConsumer reports whether a value is consumed by a scope that
	// can actually see the provider.
	hasSingleConsumer := func(n *graphNode, key depKey) bool {
		for _, consumer := range singleConsumers[key] {
			if visible(n, consumer) {
				return true
			}
		}
		return false
	}

	// groupConsumerInfo describes how a group is consumed by scopes that can
	// see the provider: whether any hard consumer exists, and whether the
	// only visible consumers are soft.
	groupConsumerInfo := func(n *graphNode, key depKey) (hasHard, hasSoft bool) {
		for _, c := range groupConsumers[key] {
			if !visible(n, c.mod) {
				continue
			}
			if c.soft {
				hasSoft = true
			} else {
				hasHard = true
			}
		}
		return hasHard, hasSoft
	}

	report := UnusedDependencyReport{Complete: true}
	for _, n := range t.nodes {
		if !n.reportable {
			continue
		}

		if !n.called {
			provider := UnusedProvider{
				Name:    n.name,
				Module:  moduleName(n.mod),
				Private: n.private,
			}
			for _, out := range n.outputs {
				provider.Outputs = append(provider.Outputs, t.uncalledOutput(n, out, hasSingleConsumer, groupConsumerInfo))
			}
			if len(provider.Outputs) > 0 {
				report.Uncalled = append(report.Uncalled, provider)
			}
			continue
		}

		// The constructor ran: report only outputs nothing consumed.
		var outputs []UnusedOutput
		for _, out := range n.outputs {
			switch out.key.kind {
			case depValue:
				if !hasSingleConsumer(n, out.key) {
					outputs = append(outputs, makeUnusedOutput(out, false, ""))
				}
			case depGroup:
				// If the constructor ran and a (hard or soft) group consumer
				// exists, the produced value was delivered into the group
				// slice, so it was consumed.
				hasHard, hasSoft := groupConsumerInfo(n, out.key)
				if !hasHard && !hasSoft {
					outputs = append(outputs, makeUnusedOutput(out, false, ""))
				}
			}
		}
		if len(outputs) > 0 {
			report.Unconsumed = append(report.Unconsumed, UnusedProvider{
				Name:    n.name,
				Module:  moduleName(n.mod),
				Private: n.private,
				Outputs: outputs,
			})
		}
	}

	return report
}

func (t *dependencyTracker) uncalledOutput(
	n *graphNode,
	out depOutput,
	hasSingleConsumer func(*graphNode, depKey) bool,
	groupConsumerInfo func(*graphNode, depKey) (bool, bool),
) UnusedOutput {
	switch out.key.kind {
	case depValue:
		// A visible consumer that did not pull the value in means the value
		// was shadowed, typically by fx.Replace or a closer-scoped provider.
		if hasSingleConsumer(n, out.key) {
			return makeUnusedOutput(out, false, "the value is requested in a visible scope but served by another (replaced or shadowed) provider")
		}
		return makeUnusedOutput(out, false, "")
	case depGroup:
		hasHard, hasSoft := groupConsumerInfo(n, out.key)
		switch {
		case hasHard:
			// A hard consumer should have pulled this constructor in. This can
			// happen when the group value was replaced/decorated away.
			return makeUnusedOutput(out, false, "the group is consumed, but this member's value was replaced or shadowed")
		case hasSoft:
			return makeUnusedOutput(out, true, softGroupNote(out.key.tag))
		default:
			return makeUnusedOutput(out, false, "")
		}
	default:
		return makeUnusedOutput(out, false, "")
	}
}

func softGroupNote(group string) string {
	return fmt.Sprintf(
		"the only consumer is the soft value group %q; soft groups only include values from constructors called for other reasons, so this constructor was not pulled in",
		group)
}

func makeUnusedOutput(out depOutput, soft bool, note string) UnusedOutput {
	o := UnusedOutput{Type: out.display, Note: note, Soft: soft}
	switch out.key.kind {
	case depValue:
		o.Name = out.key.tag
	case depGroup:
		o.Group = out.key.tag
	}
	return o
}

func moduleName(mod *module) string {
	if mod == nil {
		return ""
	}
	return mod.name
}

// visible reports whether a consumer in consumerMod can resolve a value
// produced by n. Exported providers are hoisted to the root scope and visible
// everywhere; private providers are only visible to the module they belong
// to and its descendants.
func visible(n *graphNode, consumerMod *module) bool {
	if !n.private {
		return true
	}
	for mod := consumerMod; mod != nil; mod = mod.parent {
		if mod == n.mod {
			return true
		}
	}
	return false
}

func (t *dependencyTracker) isComplete(app *App) bool {
	if app.err != nil || app.validate {
		return false
	}
	for _, inv := range t.invokes {
		if !inv.ran {
			return false
		}
	}
	return true
}

func (t *dependencyTracker) incompleteReason(app *App) string {
	switch {
	case app.validate:
		return "the application was built in validate (dry-run) mode, so no constructors were executed; unused dependencies cannot be determined"
	case app.err != nil:
		return fmt.Sprintf(
			"the application failed to assemble (%v); this report covers only the phases that completed successfully (%d of %d invocations ran), so constructors from phases that did not run are not reported as unused",
			app.err, t.invocationsRun(), t.invocationsTotal())
	default:
		// Registration succeeded but at least one invocation did not run.
		return fmt.Sprintf(
			"not all invocations ran (%d of %d); this report covers only the phases that completed successfully",
			t.invocationsRun(), t.invocationsTotal())
	}
}

func (t *dependencyTracker) invocationsRun() int {
	ran := 0
	for _, inv := range t.invokes {
		if inv.ran {
			ran++
		}
	}
	return ran
}

func (t *dependencyTracker) invocationsTotal() int {
	return len(t.invokes)
}

// parseDepOutput parses Dig's canonical rendering of an [dig.Out] value.
//
// Examples:
//
//	*pkg.Type
//	*pkg.Type[name = "x"]
//	pkg.Type[group = "g"]
func parseDepOutput(s string) depOutput {
	typ, annotation := splitTypeAnnotation(s)
	key := depKey{typ: typ}
	if name, ok := annotationValue(annotation, "name"); ok {
		key.kind = depValue
		key.tag = name
	} else if group, ok := annotationValue(annotation, "group"); ok {
		key.kind = depGroup
		key.tag = group
	}
	return depOutput{display: s, key: key}
}

// parseDepInput parses Dig's canonical rendering of a constructor input.
//
// Examples:
//
//	*pkg.Type
//	*pkg.Type[optional]
//	*pkg.Type[name = "x"]
//	[]pkg.Type[group = "g"]
func parseDepInput(s string) depInput {
	typ, annotation := splitTypeAnnotation(s)
	key := depKey{typ: typ}
	if name, ok := annotationValue(annotation, "name"); ok {
		key.kind = depValue
		key.tag = name
	} else if group, ok := annotationValue(annotation, "group"); ok {
		key.kind = depGroup
		key.tag = group
		// Dig renders a group parameter as the slice type; the graph keys
		// group members by their element type.
		key.typ = strings.TrimPrefix(key.typ, "[]")
	}
	return depInput{key: key}
}

// splitTypeAnnotation splits Dig's "Type[annotation]" rendering into the
// raw type and the annotation body. It only treats the trailing bracket pair
// as a Dig annotation when it contains Dig's tokens ("name = ...",
// "group = ...", or "optional"), which keeps generic type names such as
// pkg.Thing[int, string] intact.
func splitTypeAnnotation(s string) (typ, annotation string) {
	if !strings.HasSuffix(s, "]") {
		return s, ""
	}
	open := strings.LastIndex(s, "[")
	if open < 0 {
		return s, ""
	}
	body := s[open+1 : len(s)-1]
	if !isDigAnnotation(body) {
		return s, ""
	}
	return s[:open], body
}

func isDigAnnotation(body string) bool {
	if body == "optional" || strings.HasPrefix(body, "optional,") {
		return true
	}
	return strings.Contains(body, "=")
}

// annotationValue extracts a quoted value for a token such as
// `name = "x"` from an annotation body.
func annotationValue(annotation, token string) (string, bool) {
	if annotation == "" {
		return "", false
	}
	for _, part := range strings.Split(annotation, ",") {
		part = strings.TrimSpace(part)
		name, value, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(name) != token {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"`)
		return value, true
	}
	return "", false
}

// softGroupKeys returns the set of value-group keys consumed softly by fn.
//
// Soft groups can only be consumed through `group:"name,soft"` tags on
// fields of [In] parameter objects; Dig does not surface the soft flag in
// [dig.ProvideInfo], so it is recovered here directly from the function
// signature. The function is only inspected, never called.
func softGroupKeys(fn any) map[depKey]bool {
	if fn == nil {
		return nil
	}
	ft := reflect.TypeOf(fn)
	if ft == nil || ft.Kind() != reflect.Func {
		return nil
	}

	soft := make(map[depKey]bool)
	numIn := ft.NumIn()
	if ft.IsVariadic() {
		numIn-- // Dig ignores variadic parameters.
	}
	for i := 0; i < numIn; i++ {
		collectSoftGroupFields(ft.In(i), soft)
	}
	return soft
}

func collectSoftGroupFields(t reflect.Type, soft map[depKey]bool) {
	if t.Kind() != reflect.Struct || !isIn(t) {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Type == reflect.TypeOf(In{}) {
			continue // skip the embedded dig.In
		}

		if tag := f.Tag.Get("group"); tag != "" {
			g, err := parseGroupTag(tag)
			if err != nil {
				continue
			}
			if g.soft && f.Type.Kind() == reflect.Slice {
				soft[depKey{
					kind: depGroup,
					typ:  f.Type.Elem().String(),
					tag:  g.name,
				}] = true
			}
			continue
		}

		// Recurse into nested parameter objects.
		collectSoftGroupFields(f.Type, soft)
	}
}

type groupTag struct {
	name    string
	flatten bool
	soft    bool
}

// parseGroupTag parses a `group:"name,soft"` (or ",flatten") tag value.
func parseGroupTag(tag string) (groupTag, error) {
	parts := strings.Split(tag, ",")
	g := groupTag{name: parts[0]}
	for _, opt := range parts[1:] {
		switch strings.TrimSpace(opt) {
		case "flatten":
			g.flatten = true
		case "soft":
			g.soft = true
		case "":
		default:
			return g, fmt.Errorf("invalid group option %q", opt)
		}
	}
	return g, nil
}
