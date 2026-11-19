// Licensed to the Apache Software Foundation (ASF) under one
// or more contributor license agreements.  See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership.  The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build go1.18

package exprs

import (
	"fmt"
	"strings"

	"github.com/apache/arrow-go/v18/internal/utils/maphash"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/compute"
	"github.com/apache/arrow-go/v18/arrow/compute/exprs/internal/shared"
	"github.com/apache/arrow-go/v18/arrow/compute/exprs/internal/typeconv"
	"github.com/apache/arrow-go/v18/arrow/scalar"
	"github.com/substrait-io/substrait-go/v8/expr"
	"github.com/substrait-io/substrait-go/v8/extensions"
	"github.com/substrait-io/substrait-go/v8/types"
)

const (
	// URN for official Arrow Substrait Extension Types
	ArrowExtTypesUrn          = "https://github.com/apache/arrow/blob/main/format/substrait/extension_types.yaml"
	SubstraitDefaultURIPrefix = extensions.SubstraitDefaultURNPrefix
	// URN for official Substrait Arithmetic funcs extensions
	SubstraitArithmeticFuncsURI = SubstraitDefaultURIPrefix + "functions_arithmetic"
	// URN for official Substrait Comparison funcs extensions
	SubstraitComparisonFuncsURI = SubstraitDefaultURIPrefix + "functions_comparison"
	SubstraitBooleanFuncsURI    = SubstraitDefaultURIPrefix + "functions_boolean"
	SubstraitStringFuncsURI     = SubstraitDefaultURIPrefix + "functions_string"

	SubstraitIcebergSetFuncURN = "extension:apache.iceberg:functions_set"
	TimestampTzTimezone        = typeconv.TimestampTzTimezone
)

var hashSeed maphash.Seed

// the default extension registry that will contain the Arrow extension
// type variations and types.
var DefaultExtensionIDRegistry = NewExtensionIDRegistry()

func init() {
	hashSeed = maphash.MakeSeed()

	types := []struct {
		dt   arrow.DataType
		name string
	}{
		{arrow.PrimitiveTypes.Uint8, "u8"},
		{arrow.PrimitiveTypes.Uint16, "u16"},
		{arrow.PrimitiveTypes.Uint32, "u32"},
		{arrow.PrimitiveTypes.Uint64, "u64"},
		{arrow.FixedWidthTypes.Float16, "fp16"},
		{arrow.Null, "null"},
		{arrow.FixedWidthTypes.MonthInterval, "interval_month"},
		{arrow.FixedWidthTypes.DayTimeInterval, "interval_day_milli"},
		{arrow.FixedWidthTypes.MonthDayNanoInterval, "interval_month_day_nano"},
	}

	for _, t := range types {
		err := DefaultExtensionIDRegistry.RegisterType(extensions.ID{
			URN: ArrowExtTypesUrn, Name: t.name}, t.dt)
		if err != nil {
			panic(err)
		}
	}

	for _, fn := range []string{"add", "subtract", "multiply", "divide", "power", "sqrt", "abs"} {
		err := DefaultExtensionIDRegistry.AddSubstraitScalarToArrow(
			extensions.ID{URN: SubstraitArithmeticFuncsURI, Name: fn},
			decodeOptionlessOverflowableArithmetic(fn))
		if err != nil {
			panic(err)
		}
	}

	for _, fn := range []string{"add", "subtract", "multiply", "divide"} {
		err := DefaultExtensionIDRegistry.AddArrowToSubstrait(fn,
			encodeOptionlessOverflowableArithmetic(extensions.ID{
				URN: SubstraitArithmeticFuncsURI, Name: fn}))
		if err != nil {
			panic(err)
		}
	}

	for _, fn := range []string{"equal", "not_equal", "lt", "lte", "gt", "gte", "is_null", "is_not_null", "is_nan"} {
		err := DefaultExtensionIDRegistry.AddSubstraitScalarToArrow(
			extensions.ID{URN: SubstraitComparisonFuncsURI, Name: fn},
			simpleMapSubstraitToArrowFunc)
		if err != nil {
			panic(err)
		}
	}

	for _, fn := range []string{"equal", "not_equal", "less", "less_equal", "greater", "greater_equal", "is_null", "is_not_null", "is_nan"} {
		err := DefaultExtensionIDRegistry.AddArrowToSubstrait(fn,
			simpleMapArrowToSubstraitFunc(SubstraitComparisonFuncsURI))
		if err != nil {
			panic(err)
		}
	}

	for _, fn := range []string{"and", "or", "not"} {
		err := DefaultExtensionIDRegistry.AddSubstraitScalarToArrow(
			extensions.ID{URN: SubstraitBooleanFuncsURI, Name: fn},
			simpleMapSubstraitToArrowFunc)
		if err != nil {
			panic(err)
		}
	}

	for _, fn := range []string{"and_kleene", "or_kleene", "not"} {
		err := DefaultExtensionIDRegistry.AddArrowToSubstrait(fn,
			simpleMapArrowToSubstraitFunc(SubstraitBooleanFuncsURI))
		if err != nil {
			panic(err)
		}
	}

	for _, fn := range []string{"is_in"} {
		err := DefaultExtensionIDRegistry.AddSubstraitScalarToArrow(
			extensions.ID{URN: SubstraitIcebergSetFuncURN, Name: fn},
			setLookupFuncSubstraitToArrowFunc)
		if err != nil {
			panic(err)
		}
	}

	err := DefaultExtensionIDRegistry.AddSubstraitScalarToArrow(
		extensions.ID{URN: SubstraitStringFuncsURI, Name: "starts_with"},
		startsWithSubstraitToArrowFunc)
	if err != nil {
		panic(err)
	}

	err = DefaultExtensionIDRegistry.AddArrowToSubstrait("starts_with",
		simpleMapArrowToSubstraitFunc(SubstraitStringFuncsURI))
	if err != nil {
		panic(err)
	}
}

type substraitToArrow = func(*expr.ScalarFunction, []compute.Datum) (fname string, args []compute.Datum, opts compute.FunctionOptions, err error)
type arrowToSubstrait = func(fname string) (extensions.ID, []*types.FunctionOption, error)

func setLookupFuncSubstraitToArrowFunc(sf *expr.ScalarFunction, input []compute.Datum) (fname string, args []compute.Datum, opts compute.FunctionOptions, err error) {
	fname, _, _ = strings.Cut(sf.Name(), ":")
	f, ok := shared.SubstraitToArrowFuncMap[fname]
	if ok {
		fname = f
	}

	setopts := &compute.SetOptions{
		NullBehavior: compute.NullMatchingMatch,
	}
	switch input[1].Kind() {
	case compute.KindArray, compute.KindChunked:
		setopts.ValueSet = input[1]
	case compute.KindScalar:
		// should be a list scalar
		setopts.ValueSet = compute.NewDatumWithoutOwning(
			input[1].(*compute.ScalarDatum).Value.(*scalar.List).Value)
	}

	args, opts = input[0:1], setopts
	return
}

// startsWithSubstraitToArrowFunc maps CASE_INSENSITIVE to IgnoreCase.
// A case_sensitivity preference list with only CASE_INSENSITIVE_ASCII
// returns an error wrapping arrow.ErrNotImplemented.
func startsWithSubstraitToArrowFunc(sf *expr.ScalarFunction, input []compute.Datum) (fname string, args []compute.Datum, opts compute.FunctionOptions, err error) {
	if len(input) != 2 {
		return "", nil, nil, fmt.Errorf("%w: starts_with expects 2 arguments, got %d",
			arrow.ErrNotImplemented, len(input))
	}

	sensitivity, err := shared.ParseOption(sf.GetOption("case_sensitivity"), &shared.CaseSensitivityParser,
		[]shared.CaseSensitivity{shared.CaseSensitive, shared.CaseInsensitive}, shared.CaseSensitive)
	if err != nil {
		return "", nil, nil, fmt.Errorf("%w: starts_with case_sensitivity %v", err, sf.GetOption("case_sensitivity"))
	}

	if _, ok := sf.Arg(1).(expr.Literal); !ok {
		return "", nil, nil, fmt.Errorf("%w: starts_with pattern must be a literal", arrow.ErrNotImplemented)
	}

	sc := input[1].(*compute.ScalarDatum).Value
	if !sc.IsValid() {
		return "", nil, nil, fmt.Errorf("%w: starts_with pattern must not be null", arrow.ErrNotImplemented)
	}
	if ext, ok := sc.(*scalar.Extension); ok {
		sc = ext.Value
	}
	pattern, ok := sc.(scalar.BinaryScalar)
	if !ok {
		return "", nil, nil, fmt.Errorf("%w: starts_with pattern of type %s", arrow.ErrNotImplemented, sc.DataType())
	}

	return "starts_with", input[:1], &compute.MatchSubstringOptions{
		Pattern:    string(pattern.Data()),
		IgnoreCase: sensitivity == shared.CaseInsensitive,
	}, nil
}

func simpleMapSubstraitToArrowFunc(sf *expr.ScalarFunction, input []compute.Datum) (fname string, args []compute.Datum, opts compute.FunctionOptions, err error) {
	args = input

	fname, _, _ = strings.Cut(sf.Name(), ":")
	f, ok := shared.SubstraitToArrowFuncMap[fname]
	if ok {
		fname = f
	}
	return
}

func simpleMapArrowToSubstraitFunc(urn string) arrowToSubstrait {
	return func(fname string) (extensions.ID, []*types.FunctionOption, error) {
		f, ok := shared.ArrowToSubstraitFuncMap[fname]
		if ok {
			fname = f
		}
		return extensions.ID{URN: urn, Name: fname}, nil, nil
	}
}

func decodeOptionlessOverflowableArithmetic(n string) substraitToArrow {
	return func(sf *expr.ScalarFunction, input []compute.Datum) (fname string, args []compute.Datum, opts compute.FunctionOptions, err error) {
		overflow, err := shared.ParseOption(sf.GetOption("overflow"), &shared.OverflowParser, []shared.OverflowBehavior{shared.OverflowSilent, shared.OverflowError}, shared.OverflowSilent)
		if err != nil {
			return n, input, nil, err
		}

		switch overflow {
		case shared.OverflowSilent:
			return n + "_unchecked", input, nil, nil
		case shared.OverflowError:
			return n, input, nil, nil
		default:
			return n, input, nil, arrow.ErrNotImplemented
		}
	}
}

func encodeOptionlessOverflowableArithmetic(id extensions.ID) arrowToSubstrait {
	return func(fname string) (extensions.ID, []*types.FunctionOption, error) {
		fn, _, ok := strings.Cut(fname, ":")
		if ok {
			id.Name = fname
			fname = fn
		}

		opts := make([]*types.FunctionOption, 0, 1)
		if strings.HasSuffix(fname, "_unchecked") {
			opts = append(opts, &types.FunctionOption{
				Name: "overflow", Preference: []string{"SILENT"}})
		} else {
			opts = append(opts, &types.FunctionOption{
				Name: "overflow", Preference: []string{"ERROR"}})
		}

		return id, opts, nil
	}
}

// NewExtensionSetDefault is a convenience function to create a new extension
// set using the Default arrow extension ID registry.
//
// See NewExtensionSet for more info.
func NewExtensionSetDefault(set expr.ExtensionRegistry) ExtensionIDSet {
	return &extensionSet{ExtensionRegistry: set, reg: DefaultExtensionIDRegistry}
}

// NewExtensionSet creates a new extension set given a substrait extension registry,
// and an Arrow <--> Substrait registry for mapping substrait extensions to
// their Arrow equivalents. This extension set can then be used to manage a
// particular set of extensions in use by an expression or plan, so when
// serializing you only need to serialize the extensions that have been
// inserted into the extension set.
func NewExtensionSet(set expr.ExtensionRegistry, reg *ExtensionIDRegistry) ExtensionIDSet {
	return &extensionSet{ExtensionRegistry: set, reg: reg}
}

type extensionSet struct {
	expr.ExtensionRegistry
	reg *ExtensionIDRegistry
}

func (e *extensionSet) GetArrowRegistry() *ExtensionIDRegistry       { return e.reg }
func (e *extensionSet) GetSubstraitRegistry() expr.ExtensionRegistry { return e.ExtensionRegistry }

func (e *extensionSet) DecodeTypeArrow(anchor uint32) (extensions.ID, arrow.DataType, bool) {
	id, ok := e.DecodeType(anchor)
	if !ok {
		if id, ok = e.DecodeTypeVariation(anchor); !ok {
			return id, nil, false
		}
	}

	dt, ok := e.reg.GetTypeByID(id)
	return id, dt, ok
}

func (e *extensionSet) DecodeFunction(ref uint32) (extensions.ID, substraitToArrow, bool) {
	id, ok := e.DecodeFunc(ref)
	if !ok {
		return id, nil, false
	}

	conv, ok := e.reg.GetSubstraitScalarToArrow(id)
	if !ok {
		id.Name, _, ok = strings.Cut(id.Name, ":")
		if ok {
			conv, ok = e.reg.GetSubstraitScalarToArrow(id)
		}
	}
	return id, conv, ok
}

func (e *extensionSet) EncodeTypeVariation(dt arrow.DataType) (extensions.ID, uint32, bool) {
	id, ok := e.reg.GetIDByType(dt)
	if !ok {
		return extensions.ID{}, 0, false
	}

	return id, e.GetTypeVariationAnchor(id), true
}

func (e *extensionSet) EncodeType(dt arrow.DataType) (extensions.ID, uint32, bool) {
	id, ok := e.reg.GetIDByType(dt)
	if !ok {
		return extensions.ID{}, 0, false
	}

	return id, e.GetTypeAnchor(id), true
}

func (e *extensionSet) EncodeFunction(id extensions.ID) uint32 {
	return e.GetFuncAnchor(id)
}

// ExtensionIDRegistry manages a set of mappings between Arrow types
// and functions and their substrait equivalents.
type ExtensionIDRegistry struct {
	typeList []arrow.DataType
	ids      []extensions.ID

	substraitToIdx map[extensions.ID]int
	arrowToIdx     map[uint64]int

	substraitToArrowFn map[extensions.ID]substraitToArrow
	arrowToSubstrait   map[string]arrowToSubstrait
}

// NewExtensionIDRegistry initializes a new registry for use.
func NewExtensionIDRegistry() *ExtensionIDRegistry {
	return &ExtensionIDRegistry{
		typeList:           make([]arrow.DataType, 0),
		ids:                make([]extensions.ID, 0),
		substraitToIdx:     make(map[extensions.ID]int),
		arrowToIdx:         make(map[uint64]int),
		substraitToArrowFn: make(map[extensions.ID]substraitToArrow),
		arrowToSubstrait:   make(map[string]arrowToSubstrait),
	}
}

// RegisterType creates a mapping between the given extension ID and the
// provided Arrow data type. If this extension ID or arrow type are already
// registered, an arrow.ErrInvalid error will be returned.
func (e *ExtensionIDRegistry) RegisterType(id extensions.ID, dt arrow.DataType) error {
	if _, ok := e.substraitToIdx[id]; ok {
		return fmt.Errorf("%w: type id already registered", arrow.ErrInvalid)
	}

	dthash := arrow.HashType(hashSeed, dt)
	if _, ok := e.arrowToIdx[dthash]; ok {
		return fmt.Errorf("%w: type already registered", arrow.ErrInvalid)
	}

	idx := len(e.ids)
	e.typeList = append(e.typeList, dt)
	e.ids = append(e.ids, id)
	e.substraitToIdx[id] = idx
	e.arrowToIdx[dthash] = idx
	return nil
}

// AddSubstraitScalarToArrow creates a mapping between a given extension ID
// and a function which should return the corresponding Arrow compute function
// name along with any relevant FunctionOptions based on the ScalarFunction
// instance passed to it.
//
// Any relevant options should be parsed from the ScalarFunction's options
// and used to ensure the correct arrow compute function is used and necessary
// options are passed.
func (e *ExtensionIDRegistry) AddSubstraitScalarToArrow(id extensions.ID, toArrow substraitToArrow) error {
	if _, ok := e.substraitToArrowFn[id]; ok {
		return fmt.Errorf("%w: extension id already registered as function", arrow.ErrInvalid)
	}

	e.substraitToArrowFn[id] = toArrow
	return nil
}

// AddArrowToSubstrait creates a mapping between the provided arrow compute function
// and a function which should provide the correct substrait ExtensionID and function
// options from that name.
func (e *ExtensionIDRegistry) AddArrowToSubstrait(name string, fn arrowToSubstrait) error {
	if _, ok := e.arrowToSubstrait[name]; ok {
		return fmt.Errorf("%w: function name '%s' already registered for conversion to substrait", arrow.ErrInvalid, name)
	}

	e.arrowToSubstrait[name] = fn
	return nil
}

// GetTypeByID returns the mapped arrow data type from the provided substrait
// extension id. If no mapping exists for this substrait extension id,
// the second return value will be false.
func (e *ExtensionIDRegistry) GetTypeByID(id extensions.ID) (arrow.DataType, bool) {
	idx, ok := e.substraitToIdx[id]
	if !ok {
		return nil, false
	}

	return e.typeList[idx], true
}

// GetIDByType is the inverse of GetTypeByID, returning the mapped substrait
// extension ID corresponding to the provided arrow data type. The second
// return is false if there is no mapping found.
func (e *ExtensionIDRegistry) GetIDByType(typ arrow.DataType) (extensions.ID, bool) {
	dthash := arrow.HashType(hashSeed, typ)
	idx, ok := e.arrowToIdx[dthash]
	if !ok {
		return extensions.ID{}, false
	}

	return e.ids[idx], true
}

// GetSubstraitScalarToArrow returns the mapped conversion function for a
// given substrait extension ID to convert a substrait ScalarFunction to
// the corresponding Arrow compute function call. False is returned as
// the second value if there is no mapping available.
func (e *ExtensionIDRegistry) GetSubstraitScalarToArrow(id extensions.ID) (substraitToArrow, bool) {
	conv, ok := e.substraitToArrowFn[id]
	if !ok {
		return nil, ok
	}

	return conv, true
}

// GetArrowToSubstrait returns the mapped function to convert an arrow compute
// function to the corresponding Substrait ScalarFunction extension ID and options.
// False is returned as the second value if there is no mapping found.
func (e *ExtensionIDRegistry) GetArrowToSubstrait(name string) (conv arrowToSubstrait, ok bool) {
	conv, ok = e.arrowToSubstrait[name]
	if !ok {
		fn, _, found := strings.Cut(name, ":")
		if found {
			conv, ok = e.arrowToSubstrait[fn]
		}
	}
	return
}

// ExtensionIDSet is an interface for managing the mapping between arrow
// and substrait types and function extensions.
type ExtensionIDSet interface {
	GetArrowRegistry() *ExtensionIDRegistry
	GetSubstraitRegistry() expr.ExtensionRegistry

	DecodeTypeArrow(anchor uint32) (extensions.ID, arrow.DataType, bool)
	DecodeFunction(ref uint32) (extensions.ID, substraitToArrow, bool)

	EncodeType(dt arrow.DataType) (extensions.ID, uint32, bool)
	EncodeTypeVariation(dt arrow.DataType) (extensions.ID, uint32, bool)
}

// IsNullable is a convenience method to return whether or not
// a substrait type has Nullability set to NullabilityRequired or not.
func IsNullable(t types.Type) bool {
	return t.GetNullability() != types.NullabilityRequired
}

// FieldsFromSubstrait produces a list of arrow fields from a list of
// substrait types (such as the fields of a StructType) using nextName
// to determine the names for the fields.
func FieldsFromSubstrait(typeList []types.Type, nextName func() string, ext ExtensionIDSet) ([]arrow.Field, error) {
	converted, err := toV9Types(typeList)
	if err != nil {
		return nil, err
	}
	return typeconv.FieldsFromSubstrait(converted, nextName, typeAnchors{ext})
}

// ToSubstraitType converts an arrow data type to a Substrait Type. Since
// arrow types don't have a nullable flag (it is in the arrow.Field) but
// Substrait types do, the nullability must be passed in here.
func ToSubstraitType(dt arrow.DataType, nullable bool, ext ExtensionIDSet) (types.Type, error) {
	t, err := typeconv.ToSubstraitType(dt, nullable, typeAnchors{ext})
	if err != nil {
		return nil, err
	}
	return fromV9Type(t)
}

// FromSubstraitType returns the appropriate Arrow data type for the given
// substrait type, using the extension set if necessary.
// Since Substrait types contain their nullability also, the nullability
// returned along with the data type.
func FromSubstraitType(t types.Type, ext ExtensionIDSet) (arrow.DataType, bool, error) {
	converted, err := toV9Type(t)
	if err != nil {
		return nil, false, err
	}
	return typeconv.FromSubstraitType(converted, typeAnchors{ext})
}

// typeAnchors adapts an ExtensionIDSet to typeconv.TypeAnchors.
type typeAnchors struct{ ext ExtensionIDSet }

func (a typeAnchors) DecodeTypeArrow(anchor uint32) (arrow.DataType, bool) {
	_, dt, ok := a.ext.DecodeTypeArrow(anchor)
	return dt, ok
}

func (a typeAnchors) EncodeType(dt arrow.DataType) (uint32, bool) {
	_, anchor, ok := a.ext.EncodeType(dt)
	return anchor, ok
}

func (a typeAnchors) EncodeTypeVariation(dt arrow.DataType) (uint32, bool) {
	_, anchor, ok := a.ext.EncodeTypeVariation(dt)
	return anchor, ok
}
