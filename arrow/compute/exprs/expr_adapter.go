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

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/substrait-io/substrait-go/v8/expr"
	"github.com/substrait-io/substrait-go/v8/types"
	exprv9 "github.com/substrait-io/substrait-go/v9/expr"
	extensionsv9 "github.com/substrait-io/substrait-go/v9/extensions"
	typesv9 "github.com/substrait-io/substrait-go/v9/types"
)

// exprConverter converts the substrait-go v8 expressions that the core can
// execute to v9. Anchors are copied unchanged, so they must be resolved
// against the same anchor table (see coreExtensionSet).
type exprConverter struct {
	collection *extensionsv9.Collection
	// v8Functions maps each converted function back to its original, for
	// callbacks registered through the v8 API. Nil unless tracking.
	v8Functions map[*exprv9.ScalarFunction]*expr.ScalarFunction
}

func newExprConverter() *exprConverter {
	return &exprConverter{collection: extensionsv9.GetDefaultCollectionWithNoError()}
}

func (c *exprConverter) trackingV8Functions() *exprConverter {
	c.v8Functions = make(map[*exprv9.ScalarFunction]*expr.ScalarFunction)
	return c
}

func (c *exprConverter) expression(e expr.Expression) (exprv9.Expression, error) {
	switch e := e.(type) {
	case expr.Literal:
		return c.literal(e)
	case *expr.FieldReference:
		return c.fieldReference(e)
	case *expr.Cast:
		return c.cast(e)
	case *expr.ScalarFunction:
		return c.scalarFunction(e)
	}
	return nil, fmt.Errorf("%w: executing expression %T", arrow.ErrNotImplemented, e)
}

func (c *exprConverter) cast(e *expr.Cast) (*exprv9.Cast, error) {
	input, err := c.expression(e.Input)
	if err != nil {
		return nil, err
	}
	typ, err := toV9Type(e.Type)
	if err != nil {
		return nil, err
	}
	return &exprv9.Cast{
		Type:            typ,
		Input:           input,
		FailureBehavior: typesv9.CastFailBehavior(e.FailureBehavior),
	}, nil
}

func (c *exprConverter) scalarFunction(sf *expr.ScalarFunction) (*exprv9.ScalarFunction, error) {
	id := extensionsv9.FunctionID(sf.ID())
	declaration, ok := c.collection.GetScalarFunc(id)
	if !ok {
		declaration = extensionsv9.NewScalarFuncVariant(id)
	}

	args := make([]typesv9.FuncArg, sf.NArgs())
	for i := range args {
		var err error
		if args[i], err = c.funcArg(sf.Arg(i)); err != nil {
			return nil, err
		}
	}

	outputType, err := toV9Type(sf.GetType())
	if err != nil {
		return nil, err
	}

	converted := exprv9.NewScalarFunctionFromParts(sf.FuncRef(), declaration, args,
		toV9FunctionOptions(sf.GetOptions()), outputType)
	if c.v8Functions != nil {
		c.v8Functions[converted] = sf
	}
	return converted, nil
}

func (c *exprConverter) funcArg(arg types.FuncArg) (typesv9.FuncArg, error) {
	switch arg := arg.(type) {
	case types.Enum:
		return typesv9.Enum(arg), nil
	case expr.Expression:
		return c.expression(arg)
	case types.Type:
		return toV9Type(arg)
	}
	return nil, fmt.Errorf("%w: function argument %T", arrow.ErrNotImplemented, arg)
}

func (c *exprConverter) fieldReference(e *expr.FieldReference) (*exprv9.FieldReference, error) {
	var root exprv9.RootRefType
	switch r := e.Root.(type) {
	case nil:
		root = exprv9.RootReference
	case expr.OuterReference:
		root = exprv9.OuterReference(r)
	case expr.Expression:
		converted, err := c.expression(r)
		if err != nil {
			return nil, err
		}
		root = converted
	default:
		return nil, fmt.Errorf("%w: field reference root %T", arrow.ErrNotImplemented, r)
	}

	segment, ok := e.Reference.(expr.ReferenceSegment)
	if !ok {
		return nil, fmt.Errorf("%w: field reference %T", arrow.ErrNotImplemented, e.Reference)
	}
	ref, err := c.segment(segment)
	if err != nil {
		return nil, err
	}

	typ, err := toV9Type(e.GetType())
	if err != nil {
		return nil, err
	}
	return exprv9.NewFieldReference(root, ref, typ), nil
}

func (c *exprConverter) segment(s expr.ReferenceSegment) (exprv9.ReferenceSegment, error) {
	if s == nil {
		return nil, nil
	}

	child, err := c.segment(s.GetChild())
	if err != nil {
		return nil, err
	}

	switch s := s.(type) {
	case *expr.StructFieldRef:
		return &exprv9.StructFieldRef{Field: s.Field, Child: child}, nil
	case *expr.ListElementRef:
		return &exprv9.ListElementRef{Offset: s.Offset, Child: child}, nil
	case *expr.MapKeyRef:
		key, err := c.literal(s.MapKey)
		if err != nil {
			return nil, err
		}
		return &exprv9.MapKeyRef{MapKey: key, Child: child}, nil
	}
	return nil, fmt.Errorf("%w: reference segment %T", arrow.ErrNotImplemented, s)
}

func (c *exprConverter) literal(lit expr.Literal) (exprv9.Literal, error) {
	switch v := lit.(type) {
	case *expr.NullLiteral:
		typ, err := toV9Type(v.Type)
		if err != nil {
			return nil, err
		}
		return exprv9.NewNullLiteral(typ), nil
	case *expr.PrimitiveLiteral[bool]:
		return primitiveLiteral(v.Value, v.Type)
	case *expr.PrimitiveLiteral[int8]:
		return primitiveLiteral(v.Value, v.Type)
	case *expr.PrimitiveLiteral[int16]:
		return primitiveLiteral(v.Value, v.Type)
	case *expr.PrimitiveLiteral[int32]:
		return primitiveLiteral(v.Value, v.Type)
	case *expr.PrimitiveLiteral[int64]:
		return primitiveLiteral(v.Value, v.Type)
	case *expr.PrimitiveLiteral[float32]:
		return primitiveLiteral(v.Value, v.Type)
	case *expr.PrimitiveLiteral[float64]:
		return primitiveLiteral(v.Value, v.Type)
	case *expr.PrimitiveLiteral[string]:
		return primitiveLiteral(v.Value, v.Type)
	case *expr.PrimitiveLiteral[types.Timestamp]:
		return primitiveLiteral(typesv9.Timestamp(v.Value), v.Type)
	case *expr.PrimitiveLiteral[types.TimestampTz]:
		return primitiveLiteral(typesv9.TimestampTz(v.Value), v.Type)
	case *expr.PrimitiveLiteral[types.Date]:
		return primitiveLiteral(typesv9.Date(v.Value), v.Type)
	case *expr.PrimitiveLiteral[types.Time]:
		return primitiveLiteral(typesv9.Time(v.Value), v.Type)
	case *expr.PrimitiveLiteral[types.FixedChar]:
		return primitiveLiteral(typesv9.FixedChar(v.Value), v.Type)
	case *expr.ByteSliceLiteral[[]byte]:
		return byteSliceLiteral(v.Value, v.Type)
	case *expr.ByteSliceLiteral[types.UUID]:
		return byteSliceLiteral(typesv9.UUID(v.Value), v.Type)
	case *expr.ByteSliceLiteral[types.FixedBinary]:
		return byteSliceLiteral(typesv9.FixedBinary(v.Value), v.Type)
	case *expr.ListLiteral:
		values, typ, err := c.nestedLiteral(v.Value, v.Type)
		if err != nil {
			return nil, err
		}
		return &exprv9.ListLiteral{Value: values, Type: typ}, nil
	case *expr.StructLiteral:
		values, typ, err := c.nestedLiteral(v.Value, v.Type)
		if err != nil {
			return nil, err
		}
		return &exprv9.StructLiteral{Value: values, Type: typ}, nil
	case *expr.MapLiteral:
		return c.mapLiteral(v)
	case expr.IntervalYearToMonthLiteral:
		return exprv9.IntervalYearToMonthLiteral{Years: v.Years, Months: v.Months,
			Nullability: typesv9.Nullability(v.Nullability)}, nil
	case *expr.IntervalYearToMonthLiteral:
		return &exprv9.IntervalYearToMonthLiteral{Years: v.Years, Months: v.Months,
			Nullability: typesv9.Nullability(v.Nullability)}, nil
	case *expr.ProtoLiteral:
		return protoLiteral(v)
	}
	return nil, fmt.Errorf("%w: literal %T", arrow.ErrNotImplemented, lit)
}

func primitiveLiteral[T exprv9.PrimitiveLiteralValue](value T, t types.Type) (exprv9.Literal, error) {
	typ, err := toV9Type(t)
	if err != nil {
		return nil, err
	}
	return &exprv9.PrimitiveLiteral[T]{Value: value, Type: typ}, nil
}

func byteSliceLiteral[T ~[]byte](value T, t types.Type) (exprv9.Literal, error) {
	typ, err := toV9Type(t)
	if err != nil {
		return nil, err
	}
	return &exprv9.ByteSliceLiteral[T]{Value: value, Type: typ}, nil
}

func (c *exprConverter) nestedLiteral(values []expr.Literal, t types.Type) ([]exprv9.Literal, typesv9.Type, error) {
	typ, err := toV9Type(t)
	if err != nil {
		return nil, nil, err
	}
	out := make([]exprv9.Literal, len(values))
	for i, value := range values {
		if out[i], err = c.literal(value); err != nil {
			return nil, nil, err
		}
	}
	return out, typ, nil
}

func (c *exprConverter) mapLiteral(lit *expr.MapLiteral) (*exprv9.MapLiteral, error) {
	typ, err := toV9Type(lit.Type)
	if err != nil {
		return nil, err
	}
	out := make(exprv9.MapLiteralValue, len(lit.Value))
	for i, kv := range lit.Value {
		if out[i].Key, err = c.literal(kv.Key); err != nil {
			return nil, err
		}
		if out[i].Value, err = c.literal(kv.Value); err != nil {
			return nil, err
		}
	}
	return &exprv9.MapLiteral{Value: out, Type: typ}, nil
}

func protoLiteral(lit *expr.ProtoLiteral) (*exprv9.ProtoLiteral, error) {
	typ, err := toV9Type(lit.Type)
	if err != nil {
		return nil, err
	}

	var value any
	switch v := lit.Value.(type) {
	case []byte, string, int64:
		value = v
	case *types.IntervalYearToMonth:
		value = &typesv9.IntervalYearToMonth{Years: v.GetYears(), Months: v.GetMonths()}
	case *types.IntervalDayToSecond:
		value = &typesv9.IntervalDayToSecond{Days: v.GetDays(), Seconds: v.GetSeconds(),
			Subseconds: v.GetSubseconds(), Precision: typesv9.TimePrecision(v.GetPrecision())}
	default:
		return nil, fmt.Errorf("%w: literal value %T", arrow.ErrNotImplemented, v)
	}
	return &exprv9.ProtoLiteral{Value: value, Type: typ}, nil
}

func toV9FunctionOptions(opts []*types.FunctionOption) []*typesv9.FunctionOption {
	out := make([]*typesv9.FunctionOption, len(opts))
	for i, opt := range opts {
		out[i] = &typesv9.FunctionOption{Name: opt.Name, Preference: opt.Preference}
	}
	return out
}

func fromV9FunctionOptions(opts []*typesv9.FunctionOption) []*types.FunctionOption {
	out := make([]*types.FunctionOption, len(opts))
	for i, opt := range opts {
		out[i] = &types.FunctionOption{Name: opt.Name, Preference: opt.Preference}
	}
	return out
}
