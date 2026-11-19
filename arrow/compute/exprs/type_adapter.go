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
	"github.com/substrait-io/substrait-go/v8/types"
	typesv9 "github.com/substrait-io/substrait-go/v9/types"
)

// toV9Type converts a substrait-go v8 type to the equivalent v9 type.
// Extension anchors are copied unchanged, so they still resolve against the
// v8 extension set.
func toV9Type(t types.Type) (typesv9.Type, error) {
	n := typesv9.Nullability(t.GetNullability())
	variation := t.GetTypeVariationReference()

	switch t := t.(type) {
	case *types.BooleanType:
		return &typesv9.BooleanType{Nullability: n, TypeVariationRef: variation}, nil
	case *types.Int8Type:
		return &typesv9.Int8Type{Nullability: n, TypeVariationRef: variation}, nil
	case *types.Int16Type:
		return &typesv9.Int16Type{Nullability: n, TypeVariationRef: variation}, nil
	case *types.Int32Type:
		return &typesv9.Int32Type{Nullability: n, TypeVariationRef: variation}, nil
	case *types.Int64Type:
		return &typesv9.Int64Type{Nullability: n, TypeVariationRef: variation}, nil
	case *types.Float32Type:
		return &typesv9.Float32Type{Nullability: n, TypeVariationRef: variation}, nil
	case *types.Float64Type:
		return &typesv9.Float64Type{Nullability: n, TypeVariationRef: variation}, nil
	case *types.StringType:
		return &typesv9.StringType{Nullability: n, TypeVariationRef: variation}, nil
	case *types.BinaryType:
		return &typesv9.BinaryType{Nullability: n, TypeVariationRef: variation}, nil
	case *types.TimestampType:
		return &typesv9.TimestampType{Nullability: n, TypeVariationRef: variation}, nil
	case *types.TimestampTzType:
		return &typesv9.TimestampTzType{Nullability: n, TypeVariationRef: variation}, nil
	case *types.DateType:
		return &typesv9.DateType{Nullability: n, TypeVariationRef: variation}, nil
	case *types.TimeType:
		return &typesv9.TimeType{Nullability: n, TypeVariationRef: variation}, nil
	case *types.IntervalYearType:
		return &typesv9.IntervalYearType{Nullability: n, TypeVariationRef: variation}, nil
	case *types.UUIDType:
		return &typesv9.UUIDType{Nullability: n, TypeVariationRef: variation}, nil
	case *types.FixedCharType:
		return &typesv9.FixedCharType{Nullability: n, TypeVariationRef: variation, Length: t.Length}, nil
	case *types.VarCharType:
		return &typesv9.VarCharType{Nullability: n, TypeVariationRef: variation, Length: t.Length}, nil
	case *types.FixedBinaryType:
		return &typesv9.FixedBinaryType{Nullability: n, TypeVariationRef: variation, Length: t.Length}, nil
	case *types.DecimalType:
		return &typesv9.DecimalType{Nullability: n, TypeVariationRef: variation,
			Scale: t.Scale, Precision: t.Precision}, nil
	case *types.PrecisionTimeType:
		return &typesv9.PrecisionTimeType{Nullability: n, TypeVariationRef: variation,
			Precision: typesv9.TimePrecision(t.Precision)}, nil
	case *types.PrecisionTimestampType:
		return &typesv9.PrecisionTimestampType{Nullability: n, TypeVariationRef: variation,
			Precision: typesv9.TimePrecision(t.Precision)}, nil
	case *types.PrecisionTimestampTzType:
		return &typesv9.PrecisionTimestampTzType{PrecisionTimestampType: typesv9.PrecisionTimestampType{
			Nullability: n, TypeVariationRef: variation, Precision: typesv9.TimePrecision(t.Precision)}}, nil
	case *types.IntervalDayType:
		return &typesv9.IntervalDayType{Nullability: n, TypeVariationRef: variation,
			Precision: typesv9.TimePrecision(t.Precision)}, nil
	case *types.StructType:
		fields, err := toV9Types(t.Types)
		if err != nil {
			return nil, err
		}
		return &typesv9.StructType{Nullability: n, TypeVariationRef: variation, Types: fields}, nil
	case *types.ListType:
		elem, err := toV9Type(t.Type)
		if err != nil {
			return nil, err
		}
		return &typesv9.ListType{Nullability: n, TypeVariationRef: variation, Type: elem}, nil
	case *types.MapType:
		key, err := toV9Type(t.Key)
		if err != nil {
			return nil, err
		}
		value, err := toV9Type(t.Value)
		if err != nil {
			return nil, err
		}
		return &typesv9.MapType{Nullability: n, TypeVariationRef: variation, Key: key, Value: value}, nil
	case *types.UserDefinedType:
		params := make([]typesv9.TypeParam, len(t.TypeParameters))
		for i, p := range t.TypeParameters {
			var err error
			if params[i], err = toV9TypeParam(p); err != nil {
				return nil, err
			}
		}
		return &typesv9.UserDefinedType{Nullability: n, TypeVariationRef: variation,
			TypeReference: t.TypeReference, TypeParameters: params}, nil
	}
	return nil, fmt.Errorf("%w: substrait type %s", arrow.ErrNotImplemented, t)
}

func toV9Types(in []types.Type) ([]typesv9.Type, error) {
	out := make([]typesv9.Type, len(in))
	for i, t := range in {
		var err error
		if out[i], err = toV9Type(t); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func toV9TypeParam(p types.TypeParam) (typesv9.TypeParam, error) {
	switch p := p.(type) {
	case *types.NullParameter:
		return &typesv9.NullParameter{}, nil
	case *types.DataTypeParameter:
		t, err := toV9Type(p.Type)
		if err != nil {
			return nil, err
		}
		return &typesv9.DataTypeParameter{Type: t}, nil
	case *types.BooleanParameter:
		v := typesv9.BooleanParameter(*p)
		return &v, nil
	case *types.IntegerParameter:
		v := typesv9.IntegerParameter(*p)
		return &v, nil
	case *types.EnumParameter:
		v := typesv9.EnumParameter(*p)
		return &v, nil
	case *types.StringParameter:
		v := typesv9.StringParameter(*p)
		return &v, nil
	}
	return nil, fmt.Errorf("%w: substrait type parameter %s", arrow.ErrNotImplemented, p)
}

// fromV9Type converts a substrait-go v9 type produced by the core back to v8.
// It only needs to handle the types the core ToSubstraitType creates.
func fromV9Type(t typesv9.Type) (types.Type, error) {
	n := types.Nullability(t.GetNullability())
	variation := t.GetTypeVariationReference()

	switch t := t.(type) {
	case *typesv9.BooleanType:
		return &types.BooleanType{Nullability: n, TypeVariationRef: variation}, nil
	case *typesv9.Int8Type:
		return &types.Int8Type{Nullability: n, TypeVariationRef: variation}, nil
	case *typesv9.Int16Type:
		return &types.Int16Type{Nullability: n, TypeVariationRef: variation}, nil
	case *typesv9.Int32Type:
		return &types.Int32Type{Nullability: n, TypeVariationRef: variation}, nil
	case *typesv9.Int64Type:
		return &types.Int64Type{Nullability: n, TypeVariationRef: variation}, nil
	case *typesv9.Float32Type:
		return &types.Float32Type{Nullability: n, TypeVariationRef: variation}, nil
	case *typesv9.Float64Type:
		return &types.Float64Type{Nullability: n, TypeVariationRef: variation}, nil
	case *typesv9.StringType:
		return &types.StringType{Nullability: n, TypeVariationRef: variation}, nil
	case *typesv9.BinaryType:
		return &types.BinaryType{Nullability: n, TypeVariationRef: variation}, nil
	case *typesv9.DateType:
		return &types.DateType{Nullability: n, TypeVariationRef: variation}, nil
	case *typesv9.IntervalYearType:
		return &types.IntervalYearType{Nullability: n, TypeVariationRef: variation}, nil
	case *typesv9.UUIDType:
		return &types.UUIDType{Nullability: n, TypeVariationRef: variation}, nil
	case *typesv9.FixedCharType:
		return &types.FixedCharType{Nullability: n, TypeVariationRef: variation, Length: t.Length}, nil
	case *typesv9.VarCharType:
		return &types.VarCharType{Nullability: n, TypeVariationRef: variation, Length: t.Length}, nil
	case *typesv9.FixedBinaryType:
		return &types.FixedBinaryType{Nullability: n, TypeVariationRef: variation, Length: t.Length}, nil
	case *typesv9.DecimalType:
		return &types.DecimalType{Nullability: n, TypeVariationRef: variation,
			Scale: t.Scale, Precision: t.Precision}, nil
	case *typesv9.PrecisionTimeType:
		return &types.PrecisionTimeType{Nullability: n, TypeVariationRef: variation,
			Precision: types.TimePrecision(t.Precision)}, nil
	case *typesv9.PrecisionTimestampType:
		return &types.PrecisionTimestampType{Nullability: n, TypeVariationRef: variation,
			Precision: types.TimePrecision(t.Precision)}, nil
	case *typesv9.PrecisionTimestampTzType:
		return &types.PrecisionTimestampTzType{PrecisionTimestampType: types.PrecisionTimestampType{
			Nullability: n, TypeVariationRef: variation, Precision: types.TimePrecision(t.Precision)}}, nil
	case *typesv9.IntervalDayType:
		return &types.IntervalDayType{Nullability: n, TypeVariationRef: variation,
			Precision: types.TimePrecision(t.Precision)}, nil
	case *typesv9.StructType:
		fields := make([]types.Type, len(t.Types))
		for i, f := range t.Types {
			var err error
			if fields[i], err = fromV9Type(f); err != nil {
				return nil, err
			}
		}
		return &types.StructType{Nullability: n, TypeVariationRef: variation, Types: fields}, nil
	case *typesv9.ListType:
		elem, err := fromV9Type(t.Type)
		if err != nil {
			return nil, err
		}
		return &types.ListType{Nullability: n, TypeVariationRef: variation, Type: elem}, nil
	case *typesv9.MapType:
		key, err := fromV9Type(t.Key)
		if err != nil {
			return nil, err
		}
		value, err := fromV9Type(t.Value)
		if err != nil {
			return nil, err
		}
		return &types.MapType{Nullability: n, TypeVariationRef: variation, Key: key, Value: value}, nil
	case *typesv9.UserDefinedType:
		if len(t.TypeParameters) > 0 {
			return nil, fmt.Errorf("%w: user defined type parameters", arrow.ErrNotImplemented)
		}
		return &types.UserDefinedType{Nullability: n, TypeVariationRef: variation,
			TypeReference: t.TypeReference}, nil
	}
	return nil, fmt.Errorf("%w: substrait-go v9 type %s", arrow.ErrNotImplemented, t)
}
