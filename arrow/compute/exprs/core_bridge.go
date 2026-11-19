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
	"github.com/apache/arrow-go/v18/arrow/compute"
	core "github.com/apache/arrow-go/v18/arrow/compute/exprs/v9"
	"github.com/substrait-io/substrait-go/v8/extensions"
	exprv9 "github.com/substrait-io/substrait-go/v9/expr"
	extensionsv9 "github.com/substrait-io/substrait-go/v9/extensions"
)

// coreExtensionSet returns an exprs/v9 extension set that reads and
// allocates anchors in ext's v8 anchor table, so converted types and
// functions keep their anchors unchanged.
//
// The core resolves functions and types against the default substrait-go v9
// collection, not the collection held by ext's v8 registry.
func coreExtensionSet(ext ExtensionIDSet) core.ExtensionIDSet {
	if ext == nil {
		return nil
	}
	registry := exprv9.NewExtensionRegistry(
		anchorSet{ext.GetSubstraitRegistry().Set},
		extensionsv9.GetDefaultCollectionWithNoError())
	return core.NewExtensionSet(registry, ext.GetArrowRegistry().core)
}

// executionSet is the exprs/v9 extension set used to execute a converted v8
// expression. Functions with a mapping added through the v8 API are routed
// to their callback with the original v8 ScalarFunction.
type executionSet struct {
	core.ExtensionIDSet
	ext       ExtensionIDSet
	converter *exprConverter
}

func newExecutionSet(ext ExtensionIDSet) *executionSet {
	return &executionSet{
		ExtensionIDSet: coreExtensionSet(ext),
		ext:            ext,
		converter:      newExprConverter().trackingV8Functions(),
	}
}

func (e *executionSet) DecodeFunction(ref uint32) (core.FunctionID, func(*exprv9.ScalarFunction, []compute.Datum) (string, []compute.Datum, compute.FunctionOptions, error), bool) {
	id, ok := e.ext.GetSubstraitRegistry().DecodeFunc(ref)
	if !ok {
		return core.FunctionID{}, nil, false
	}

	conv, ok := e.ext.GetArrowRegistry().customSubstraitToArrow(id)
	if !ok {
		return e.ExtensionIDSet.DecodeFunction(ref)
	}
	return core.FunctionID(id), func(sf *exprv9.ScalarFunction, args []compute.Datum) (string, []compute.Datum, compute.FunctionOptions, error) {
		return conv(e.converter.v8Functions[sf], args)
	}, true
}

// anchorSet exposes a substrait-go v8 anchor table as a v9 extensions.Set.
type anchorSet struct {
	set extensions.Set
}

func (a anchorSet) DecodeTypeVariation(anchor uint32) (extensionsv9.TypeVariationID, bool) {
	id, ok := a.set.DecodeTypeVariation(anchor)
	return extensionsv9.TypeVariationID(id), ok
}

func (a anchorSet) DecodeFunc(anchor uint32) (extensionsv9.FunctionID, bool) {
	id, ok := a.set.DecodeFunc(anchor)
	return extensionsv9.FunctionID(id), ok
}

func (a anchorSet) DecodeType(anchor uint32) (extensionsv9.TypeID, bool) {
	id, ok := a.set.DecodeType(anchor)
	return extensionsv9.TypeID(id), ok
}

func (a anchorSet) LookupTypeVariation(anchor uint32, c *extensionsv9.Collection) (extensionsv9.TypeVariation, bool) {
	id, ok := a.DecodeTypeVariation(anchor)
	if !ok {
		return extensionsv9.TypeVariation{}, false
	}
	return c.GetTypeVariation(id)
}

func (a anchorSet) LookupType(anchor uint32, c *extensionsv9.Collection) (extensionsv9.Type, bool) {
	id, ok := a.DecodeType(anchor)
	if !ok {
		return extensionsv9.Type{}, false
	}
	return c.GetType(id)
}

func (a anchorSet) LookupScalarFunction(anchor uint32, c *extensionsv9.Collection) (*extensionsv9.ScalarFunctionVariant, bool) {
	id, ok := a.DecodeFunc(anchor)
	if !ok {
		return nil, false
	}
	return c.GetScalarFunc(id)
}

func (a anchorSet) LookupAggregateFunction(anchor uint32, c *extensionsv9.Collection) (*extensionsv9.AggregateFunctionVariant, bool) {
	id, ok := a.DecodeFunc(anchor)
	if !ok {
		return nil, false
	}
	return c.GetAggregateFunc(id)
}

func (a anchorSet) LookupWindowFunction(anchor uint32, c *extensionsv9.Collection) (*extensionsv9.WindowFunctionVariant, bool) {
	id, ok := a.DecodeFunc(anchor)
	if !ok {
		return nil, false
	}
	return c.GetWindowFunc(id)
}

func (a anchorSet) FindURN(urn string) (uint32, bool) { return a.set.FindURN(urn) }

func (a anchorSet) GetTypeAnchor(id extensionsv9.TypeID) uint32 {
	return a.set.GetTypeAnchor(extensions.ID(id))
}

func (a anchorSet) GetFuncAnchor(id extensionsv9.FunctionID) uint32 {
	return a.set.GetFuncAnchor(extensions.ID(id))
}

func (a anchorSet) GetTypeVariationAnchor(id extensionsv9.TypeVariationID) uint32 {
	return a.set.GetTypeVariationAnchor(extensions.ID(id))
}

// The core never serializes, so the anchor listings used by the v9 wire
// package are not needed.

func (anchorSet) URNs() map[uint32]string                                 { return nil }
func (anchorSet) Types() map[extensionsv9.TypeID]uint32                   { return nil }
func (anchorSet) TypeVariations() map[extensionsv9.TypeVariationID]uint32 { return nil }
func (anchorSet) Functions() map[extensionsv9.FunctionID]uint32           { return nil }
