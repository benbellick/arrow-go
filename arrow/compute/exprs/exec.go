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

package exprs

import (
	"context"
	"fmt"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/compute"
	core "github.com/apache/arrow-go/v18/arrow/compute/exprs/v9"
	"github.com/substrait-io/substrait-go/v8/expr"
	"github.com/substrait-io/substrait-go/v8/extensions"
	"github.com/substrait-io/substrait-go/v8/types"
)

// ToArrowSchema takes a substrait NamedStruct and an extension set (for
// type resolution mapping) and creates the equivalent Arrow Schema.
func ToArrowSchema(base types.NamedStruct, ext ExtensionIDSet) (*arrow.Schema, error) {
	fields := make([]arrow.Field, len(base.Names))
	for i, typ := range base.Struct.Types {
		dt, nullable, err := FromSubstraitType(typ, ext)
		if err != nil {
			return nil, err
		}
		fields[i] = arrow.Field{
			Name:     base.Names[i],
			Type:     dt,
			Nullable: nullable,
		}
	}

	return arrow.NewSchema(fields, nil), nil
}

type (
	regCtxKey struct{}
	extCtxKey struct{}
)

func WithExtensionRegistry(ctx context.Context, reg *ExtensionIDRegistry) context.Context {
	return context.WithValue(ctx, regCtxKey{}, reg)
}

func GetExtensionRegistry(ctx context.Context) *ExtensionIDRegistry {
	v, ok := ctx.Value(regCtxKey{}).(*ExtensionIDRegistry)
	if !ok {
		v = DefaultExtensionIDRegistry
	}
	return v
}

func WithExtensionIDSet(ctx context.Context, ext ExtensionIDSet) context.Context {
	return context.WithValue(ctx, extCtxKey{}, ext)
}

func GetExtensionIDSet(ctx context.Context) ExtensionIDSet {
	v, ok := ctx.Value(extCtxKey{}).(ExtensionIDSet)
	if !ok {
		return NewExtensionSet(
			expr.NewEmptyExtensionRegistry(extensions.GetDefaultCollectionWithNoError()),
			GetExtensionRegistry(ctx))
	}
	return v
}

// ExecuteScalarExpression executes the given substrait expression using the provided datum as input.
// It will first create an exec batch using the input schema and the datum.
// The datum may have missing or incorrectly ordered columns while the input schema
// should describe the expected input schema for the expression. Missing fields will
// be replaced with null scalars and incorrectly ordered columns will be re-ordered
// according to the schema.
//
// You can provide an allocator to use through the context via compute.WithAllocator.
//
// You can provide the ExtensionIDSet to use through the context via WithExtensionIDSet.
//
// Note: Substrait expressions do not have an equivalent to LargeString/LargeBinary, so
// field references which expect a string or binary will allow their Large counterparts
// to be used as well. Most of the time, this shouldn't be an issue as the compute kernels
// should handle any casting that might be necessary. Likewise, Substrait has no
// dictionary type, so a dictionary field of string or binary values (including their
// Large counterparts) whose value type matches is returned undecoded.
func ExecuteScalarExpression(ctx context.Context, inputSchema *arrow.Schema, expression expr.Expression, partialInput compute.Datum) (compute.Datum, error) {
	if expression == nil {
		return nil, arrow.ErrInvalid
	}

	ext := newExecutionSet(GetExtensionIDSet(ctx))
	converted, err := ext.converter.expression(expression)
	if err != nil {
		return nil, err
	}

	return core.ExecuteScalarExpression(core.WithExtensionIDSet(ctx, ext), inputSchema, converted, partialInput)
}

// ExecuteScalarSubstrait uses the provided Substrait extended expression to
// determine the expected input schema (replacing missing fields in the partial
// input datum with null scalars and re-ordering columns if necessary) and
// ExtensionIDSet to use. You can provide the extension registry to use
// through the context via WithExtensionRegistry, otherwise the default
// Arrow registry will be used. You can provide a memory.Allocator to use
// the same way via compute.WithAllocator.
func ExecuteScalarSubstrait(ctx context.Context, expression *expr.Extended, partialInput compute.Datum) (compute.Datum, error) {
	if expression == nil {
		return nil, arrow.ErrInvalid
	}

	var toExecute expr.Expression

	switch len(expression.ReferredExpr) {
	case 0:
		return nil, fmt.Errorf("%w: no referred expression to execute", arrow.ErrInvalid)
	case 1:
		if toExecute = expression.ReferredExpr[0].GetExpr(); toExecute == nil {
			return nil, fmt.Errorf("%w: measures not implemented", arrow.ErrNotImplemented)
		}
	default:
		return nil, fmt.Errorf("%w: only single referred expression implemented", arrow.ErrNotImplemented)
	}

	reg := GetExtensionRegistry(ctx)
	set := NewExtensionSet(expr.NewExtensionRegistry(expression.Extensions,
		extensions.GetDefaultCollectionWithNoError()), reg)
	sc, err := ToArrowSchema(expression.BaseSchema, set)
	if err != nil {
		return nil, err
	}

	return ExecuteScalarExpression(WithExtensionIDSet(ctx, set), sc, toExecute, partialInput)
}
