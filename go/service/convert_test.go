package service

import (
	"reflect"
	"testing"
	"time"

	router "github.com/openabstractions/abstraction-router/go"
	wire "github.com/openabstractions/abstraction-router/go/abstraction/router"
)

// TestToWireAliasFieldParity guards the copy toWireAlias makes from the
// hand-written router.Alias onto the generated wire.Alias. A field added to
// either struct without toWireAlias updated to carry it fails this test by
// name instead of the field dropping out of the wire response unnoticed, as
// context_length once did.
func TestToWireAliasFieldParity(t *testing.T) {
	checkFieldParity(t, toWireAlias, nil)
}

// TestToWireFamilyFieldParity guards toWireFamily's copy from router.Family
// onto wire.Family, including the nested Names conversion through
// toWireAlias and the nested Components conversion through toWireComponent.
func TestToWireFamilyFieldParity(t *testing.T) {
	checkFieldParity(t, toWireFamily, nil)
}

// TestToWireComponentFieldParity guards toWireComponent's copy from
// router.Component onto wire.Component.
func TestToWireComponentFieldParity(t *testing.T) {
	checkFieldParity(t, toWireComponent, nil)
}

// TestToWireHostStateFieldParity guards toWireHostState's copy from
// router.HostState onto wire.HostState, including the Installed int-to-int64
// widening.
func TestToWireHostStateFieldParity(t *testing.T) {
	checkFieldParity(t, toWireHostState, nil)
}

// TestToWireAskFieldParity guards toWireAsk's copy from router.Ask onto
// wire.Ask, including At's time.Time-to-formatted-string conversion.
func TestToWireAskFieldParity(t *testing.T) {
	checkFieldParity(t, toWireAsk, nil)
}

// TestToWireDecisionFieldParity guards toWireDecision's copy from
// router.Decision onto wire.Decision, including the Loads int-to-int64
// widening and Authorised's move from a string-slice pointer to a
// HostAllowance pointer.
func TestToWireDecisionFieldParity(t *testing.T) {
	checkFieldParity(t, toWireDecision, nil)
}

// TestFromWirePickRequestFieldParity guards fromWirePickRequest's copy from
// wire.PickRequest onto router.Request, the reverse direction from the
// toWire* tests above: In is the generated wire struct and Out is the
// hand-written router one. Op has no wire counterpart, computed as
// router.OpRoute instead of carried. Hosts and Allowed name the same
// restriction under different field names — Allowed wraps a HostAllowance,
// Hosts is the plain pointer-to-slice router.Request already uses for every
// operation — so both are allowlisted and
// TestFromWirePickRequestCarriesAllowedHosts checks that conversion by value
// instead of by field name.
func TestFromWirePickRequestFieldParity(t *testing.T) {
	checkFieldParity(t, fromWirePickRequest, map[string]string{
		"Op":      "every Pick call performs router.OpRoute; the wire request carries no operation to convert",
		"Hosts":   "carries wire.PickRequest's Allowed *HostAllowance under router.Request's existing field name; TestFromWirePickRequestCarriesAllowedHosts checks the value",
		"Allowed": "carried onto router.Request's Hosts field under a different name; TestFromWirePickRequestCarriesAllowedHosts checks the value",
	})
}

// TestFromWirePickRequestCarriesAllowedHosts checks the one conversion the
// field parity test allowlists instead of comparing by name: Allowed's host
// list reaches Hosts unchanged, and a nil Allowed leaves Hosts nil rather
// than an authorised empty list.
func TestFromWirePickRequestCarriesAllowedHosts(t *testing.T) {
	got := fromWirePickRequest(wire.PickRequest{Allowed: &wire.HostAllowance{Hosts: []string{"lemonade", "ollama"}}})
	if got.Hosts == nil || !reflect.DeepEqual(*got.Hosts, []string{"lemonade", "ollama"}) {
		t.Fatalf("hosts %+v", got.Hosts)
	}
	if none := fromWirePickRequest(wire.PickRequest{}); none.Hosts != nil {
		t.Fatalf("nil Allowed should leave Hosts nil, got %+v", none.Hosts)
	}
}

// checkFieldParity fills a zero value of In with fill, runs it through
// convert, and checks that every field the resulting Out carries traces back
// to the same-named field on In by equalField, and that no field on either
// side goes unaccounted for. A field name in allow has no counterpart on the
// other struct on purpose; its value is a one-line reason shown in a test
// failure if the field's name later stops matching the reason (for example
// because the field was removed), so a stale allowlist entry is still
// visible instead of silently hiding a real gap.
func checkFieldParity[In, Out any](t *testing.T, convert func(In) Out, allow map[string]string) {
	t.Helper()
	var in In
	inVal := reflect.ValueOf(&in).Elem()
	fillTop(t, inVal)

	outVal := reflect.ValueOf(convert(in))
	outType := outVal.Type()
	inType := inVal.Type()

	carried := map[string]bool{}
	for i := 0; i < outType.NumField(); i++ {
		name := outType.Field(i).Name
		carried[name] = true
		src := inVal.FieldByName(name)
		if !src.IsValid() {
			if reason, ok := allow[name]; ok {
				t.Logf("%s field %q has no counterpart on %s: %s", outType, name, inType, reason)
				continue
			}
			t.Errorf("%s field %q has no matching field on %s; add the conversion or allowlist it with a reason", outType, name, inType)
			continue
		}
		if !equalField(t, name, outVal.Field(i), src) {
			t.Errorf("field %q: conversion produced %#v, %s held %#v", name, outVal.Field(i).Interface(), inType, src.Interface())
		}
	}
	for i := 0; i < inType.NumField(); i++ {
		name := inType.Field(i).Name
		if carried[name] {
			continue
		}
		if reason, ok := allow[name]; ok {
			t.Logf("%s field %q has no counterpart on %s: %s", inType, name, outType, reason)
			continue
		}
		t.Errorf("%s field %q has no counterpart on %s; the conversion must carry it or the field must be removed", inType, name, outType)
	}
}

// equalField reports whether outVal (a field on a generated wire.* struct)
// correctly carries inVal (the same-named field on the internal router.*
// struct a toWire* function copied it from). Most fields share their Go type
// and compare by reflect.DeepEqual. A few widen or reshape in the
// conversion, so this checks those by name against the same rule the
// conversion applies, instead of reflect.DeepEqual panicking on two
// different types:
//   - Installed, Loads: router's int against wire's int64.
//   - At: router.Ask's time.Time against wire.Ask's askTimeLayout string.
//   - Authorised: router.Decision's *[]string against wire.Decision's
//     *wire.HostAllowance.
//   - Names: router.Family's []router.Alias against wire.Family's
//     []wire.Alias, element by element through toWireAlias.
func equalField(t *testing.T, fieldName string, outVal, inVal reflect.Value) bool {
	t.Helper()
	switch fieldName {
	case "Installed", "Loads":
		return outVal.Int() == inVal.Int()
	case "At":
		got, err := time.Parse(askTimeLayout, outVal.String())
		if err != nil {
			t.Errorf("field %q: wire value %q does not parse as askTimeLayout: %v", fieldName, outVal.String(), err)
			return false
		}
		want, ok := inVal.Interface().(time.Time)
		if !ok {
			t.Fatalf("field %q: expected time.Time on the internal side, got %s", fieldName, inVal.Type())
		}
		return got.Equal(want)
	case "Authorised":
		in, ok := inVal.Interface().(*[]string)
		if !ok {
			t.Fatalf("field %q: expected *[]string on the internal side, got %s", fieldName, inVal.Type())
		}
		out, ok := outVal.Interface().(*wire.HostAllowance)
		if !ok {
			t.Fatalf("field %q: expected *wire.HostAllowance on the wire side, got %s", fieldName, outVal.Type())
		}
		if in == nil || out == nil {
			return in == nil && out == nil
		}
		return reflect.DeepEqual(out.Hosts, *in)
	case "Names":
		in, ok := inVal.Interface().([]router.Alias)
		if !ok {
			t.Fatalf("field %q: expected []router.Alias on the internal side, got %s", fieldName, inVal.Type())
		}
		out, ok := outVal.Interface().([]wire.Alias)
		if !ok {
			t.Fatalf("field %q: expected []wire.Alias on the wire side, got %s", fieldName, outVal.Type())
		}
		if len(in) != len(out) {
			return false
		}
		for i := range in {
			if !reflect.DeepEqual(out[i], toWireAlias(in[i])) {
				return false
			}
		}
		return true
	case "Components":
		in, ok := inVal.Interface().([]router.Component)
		if !ok {
			t.Fatalf("field %q: expected []router.Component on the internal side, got %s", fieldName, inVal.Type())
		}
		out, ok := outVal.Interface().([]wire.Component)
		if !ok {
			t.Fatalf("field %q: expected []wire.Component on the wire side, got %s", fieldName, outVal.Type())
		}
		if len(in) != len(out) {
			return false
		}
		for i := range in {
			if !reflect.DeepEqual(out[i], toWireComponent(in[i])) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(outVal.Interface(), inVal.Interface())
	}
}

// fillTop fills every field of the struct v points at, for use as the "in"
// side of a conversion parity test.
func fillTop(t *testing.T, v reflect.Value) {
	t.Helper()
	next := 1
	fillStruct(t, v, &next)
}

// fillStruct fills every field of struct value v in turn, threading next so
// sibling and nested fields land on different values.
func fillStruct(t *testing.T, v reflect.Value, next *int) {
	t.Helper()
	for i := 0; i < v.NumField(); i++ {
		fill(t, v.Field(i), next)
	}
}

// fill sets v to a distinct non-zero value for its kind, recursing into
// nested structs, slices of structs, and pointers, so a dropped copy in a
// conversion function shows up as a mismatch rather than two zero values
// agreeing by accident. next is a shared counter threaded through the whole
// struct being filled, so no two fields collide on the same value.
//
// A defined type whose underlying kind is string — the pattern the generator
// uses for an open-vocabulary enum such as wire.ServiceErrorCode, a plain
// string plus constants and a Known() method — takes the String case below
// like any other string field: reflect.Value.Kind() reports String for it
// regardless of the named type, and SetString sets it correctly. None of the
// record types in this file's tests carry a field of one of those generated
// enum types today; if one gains one, it fills the same way.
func fill(t *testing.T, v reflect.Value, next *int) {
	t.Helper()
	seed := *next
	*next++
	switch v.Kind() {
	case reflect.String:
		v.SetString("v" + string(rune('a'+seed%26)))
	case reflect.Bool:
		// bool's only non-zero value is true; alternating with the zero value
		// would let a dropped copy agree with the unfilled field by accident.
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(seed))
	case reflect.Slice:
		switch v.Type().Elem().Kind() {
		case reflect.String:
			v.Set(reflect.ValueOf([]string{"x", "y"}))
		case reflect.Struct:
			elem := reflect.New(v.Type().Elem()).Elem()
			fillStruct(t, elem, next)
			v.Set(reflect.Append(v, elem))
		default:
			t.Fatalf("convert_test.go: fill does not know slice element kind %s for field type %s; extend fill before trusting this test", v.Type().Elem().Kind(), v.Type())
		}
	case reflect.Ptr:
		switch v.Type().Elem().Kind() {
		case reflect.Slice:
			inner := reflect.New(v.Type().Elem())
			fill(t, inner.Elem(), next)
			v.Set(inner)
		case reflect.Struct:
			inner := reflect.New(v.Type().Elem())
			fillStruct(t, inner.Elem(), next)
			v.Set(inner)
		default:
			t.Fatalf("convert_test.go: fill does not know pointer element kind %s for field type %s; extend fill before trusting this test", v.Type().Elem().Kind(), v.Type())
		}
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			v.Set(reflect.ValueOf(time.Date(2024, time.January, 2, 3, 4, 5, seed*1000, time.UTC)))
			return
		}
		fillStruct(t, v, next)
	default:
		t.Fatalf("convert_test.go: fill does not know kind %s for field type %s; extend fill before trusting this test", v.Kind(), v.Type())
	}
}
