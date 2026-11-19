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

// Package exprs is the substrait-go v8 API. It converts its inputs to
// substrait-go v9 and delegates to the implementation in exprs/v9.
package exprs

import (
	"fmt"
	"strings"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/compute"
	core "github.com/apache/arrow-go/v18/arrow/compute/exprs/v9"
	"github.com/substrait-io/substrait-go/v8/expr"
	"github.com/substrait-io/substrait-go/v8/extensions"
	"github.com/substrait-io/substrait-go/v8/types"
)

const (
	// URN for official Arrow Substrait Extension Types
	ArrowExtTypesUrn          = core.ArrowExtTypesUrn
	SubstraitDefaultURIPrefix = core.SubstraitDefaultURIPrefix
	// URN for official Substrait Arithmetic funcs extensions
	SubstraitArithmeticFuncsURI = core.SubstraitArithmeticFuncsURI
	// URN for official Substrait Comparison funcs extensions
	SubstraitComparisonFuncsURI = core.SubstraitComparisonFuncsURI
	SubstraitBooleanFuncsURI    = core.SubstraitBooleanFuncsURI
	SubstraitStringFuncsURI     = core.SubstraitStringFuncsURI

	SubstraitIcebergSetFuncURN = core.SubstraitIcebergSetFuncURN
	TimestampTzTimezone        = core.TimestampTzTimezone
)

// the default extension registry that will contain the Arrow extension
// type variations and types. It shares its types and built-in function
// mappings with the exprs/v9 default registry.
var DefaultExtensionIDRegistry = wrapRegistry(core.DefaultExtensionIDRegistry)

type substraitToArrow = func(*expr.ScalarFunction, []compute.Datum) (fname string, args []compute.Datum, opts compute.FunctionOptions, err error)
type arrowToSubstrait = func(fname string) (extensions.ID, []*types.FunctionOption, error)

// NewExtensionSetDefault is a convenience function to create a new extension
// set using the Default arrow extension ID registry.
//
// See NewExtensionSet for more info.
func NewExtensionSetDefault(set expr.ExtensionRegistry) ExtensionIDSet {
	return &extensionSet{ExtensionRegistry: set, reg: DefaultExtensionIDRegistry}
}

// NewDefaultExtensionSet constructs an empty extension set using the default
// Arrow Extension registry and the default collection of substrait extensions
// from the Substrait-go repo.
func NewDefaultExtensionSet() ExtensionIDSet {
	return NewExtensionSetDefault(
		expr.NewEmptyExtensionRegistry(extensions.GetDefaultCollectionWithNoError()))
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
	id, dt, ok := coreExtensionSet(e).DecodeTypeArrow(anchor)
	return extensions.ID(id), dt, ok
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
	id, anchor, ok := coreExtensionSet(e).EncodeTypeVariation(dt)
	return extensions.ID(id), anchor, ok
}

func (e *extensionSet) EncodeType(dt arrow.DataType) (extensions.ID, uint32, bool) {
	id, anchor, ok := coreExtensionSet(e).EncodeType(dt)
	return extensions.ID(id), anchor, ok
}

func (e *extensionSet) EncodeFunction(id extensions.ID) uint32 {
	return e.GetFuncAnchor(id)
}

// ExtensionIDRegistry manages a set of mappings between Arrow types
// and functions and their substrait equivalents.
//
// Types and the built-in function mappings live in the wrapped exprs/v9
// registry. Function mappings added through this API are kept here, since
// their callbacks use substrait-go v8 types.
type ExtensionIDRegistry struct {
	core *core.ExtensionIDRegistry

	substraitToArrowFn map[extensions.ID]substraitToArrow
	arrowToSubstrait   map[string]arrowToSubstrait
}

// NewExtensionIDRegistry initializes a new registry for use.
func NewExtensionIDRegistry() *ExtensionIDRegistry {
	return wrapRegistry(core.NewExtensionIDRegistry())
}

func wrapRegistry(reg *core.ExtensionIDRegistry) *ExtensionIDRegistry {
	return &ExtensionIDRegistry{
		core:               reg,
		substraitToArrowFn: make(map[extensions.ID]substraitToArrow),
		arrowToSubstrait:   make(map[string]arrowToSubstrait),
	}
}

// RegisterType creates a mapping between the given extension ID and the
// provided Arrow data type. If this extension ID or arrow type are already
// registered, an arrow.ErrInvalid error will be returned.
func (e *ExtensionIDRegistry) RegisterType(id extensions.ID, dt arrow.DataType) error {
	return e.core.RegisterType(core.ExtensionID(id), dt)
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
	_, isBuiltIn := e.core.GetSubstraitScalarToArrow(core.FunctionID(id))
	if _, ok := e.substraitToArrowFn[id]; ok || isBuiltIn {
		return fmt.Errorf("%w: extension id already registered as function", arrow.ErrInvalid)
	}

	e.substraitToArrowFn[id] = toArrow
	return nil
}

// AddArrowToSubstrait creates a mapping between the provided arrow compute function
// and a function which should provide the correct substrait ExtensionID and function
// options from that name.
func (e *ExtensionIDRegistry) AddArrowToSubstrait(name string, fn arrowToSubstrait) error {
	_, isBuiltIn := e.core.GetArrowToSubstrait(name)
	if _, ok := e.arrowToSubstrait[name]; ok || isBuiltIn {
		return fmt.Errorf("%w: function name '%s' already registered for conversion to substrait", arrow.ErrInvalid, name)
	}

	e.arrowToSubstrait[name] = fn
	return nil
}

// GetTypeByID returns the mapped arrow data type from the provided substrait
// extension id. If no mapping exists for this substrait extension id,
// the second return value will be false.
func (e *ExtensionIDRegistry) GetTypeByID(id extensions.ID) (arrow.DataType, bool) {
	return e.core.GetTypeByID(core.ExtensionID(id))
}

// GetIDByType is the inverse of GetTypeByID, returning the mapped substrait
// extension ID corresponding to the provided arrow data type. The second
// return is false if there is no mapping found.
func (e *ExtensionIDRegistry) GetIDByType(typ arrow.DataType) (extensions.ID, bool) {
	id, ok := e.core.GetIDByType(typ)
	return extensions.ID(id), ok
}

// GetSubstraitScalarToArrow returns the mapped conversion function for a
// given substrait extension ID to convert a substrait ScalarFunction to
// the corresponding Arrow compute function call. False is returned as
// the second value if there is no mapping available.
func (e *ExtensionIDRegistry) GetSubstraitScalarToArrow(id extensions.ID) (substraitToArrow, bool) {
	if conv, ok := e.substraitToArrowFn[id]; ok {
		return conv, true
	}

	conv, ok := e.core.GetSubstraitScalarToArrow(core.FunctionID(id))
	if !ok {
		return nil, false
	}
	return func(sf *expr.ScalarFunction, args []compute.Datum) (string, []compute.Datum, compute.FunctionOptions, error) {
		converted, err := newExprConverter().scalarFunction(sf)
		if err != nil {
			return "", nil, nil, err
		}
		return conv(converted, args)
	}, true
}

// GetArrowToSubstrait returns the mapped function to convert an arrow compute
// function to the corresponding Substrait ScalarFunction extension ID and options.
// False is returned as the second value if there is no mapping found.
func (e *ExtensionIDRegistry) GetArrowToSubstrait(name string) (arrowToSubstrait, bool) {
	if conv, ok := e.customArrowToSubstrait(name); ok {
		return conv, true
	}

	conv, ok := e.core.GetArrowToSubstrait(name)
	if !ok {
		return nil, false
	}
	return func(fname string) (extensions.ID, []*types.FunctionOption, error) {
		id, opts, err := conv(fname)
		return extensions.ID(id), fromV9FunctionOptions(opts), err
	}, true
}

func (e *ExtensionIDRegistry) customArrowToSubstrait(name string) (arrowToSubstrait, bool) {
	if conv, ok := e.arrowToSubstrait[name]; ok {
		return conv, true
	}
	if fn, _, found := strings.Cut(name, ":"); found {
		conv, ok := e.arrowToSubstrait[fn]
		return conv, ok
	}
	return nil, false
}

// customSubstraitToArrow returns a mapping added through
// AddSubstraitScalarToArrow, falling back to the function's simple name.
func (e *ExtensionIDRegistry) customSubstraitToArrow(id extensions.ID) (substraitToArrow, bool) {
	if conv, ok := e.substraitToArrowFn[id]; ok {
		return conv, true
	}
	if name, _, found := strings.Cut(id.Name, ":"); found {
		conv, ok := e.substraitToArrowFn[extensions.ID{URN: id.URN, Name: name}]
		return conv, ok
	}
	return nil, false
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
	return core.FieldsFromSubstrait(converted, nextName, coreExtensionSet(ext))
}

// ToSubstraitType converts an arrow data type to a Substrait Type. Since
// arrow types don't have a nullable flag (it is in the arrow.Field) but
// Substrait types do, the nullability must be passed in here.
func ToSubstraitType(dt arrow.DataType, nullable bool, ext ExtensionIDSet) (types.Type, error) {
	converted, err := core.ToSubstraitType(dt, nullable, coreExtensionSet(ext))
	if err != nil {
		return nil, err
	}
	return fromV9Type(converted)
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
	return core.FromSubstraitType(converted, coreExtensionSet(ext))
}
