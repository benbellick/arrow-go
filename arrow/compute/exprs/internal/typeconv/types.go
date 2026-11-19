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

// Package typeconv converts between Arrow and Substrait types. It is written
// against the newest supported substrait-go major version; packages for older
// versions convert their types to this version before calling it.
package typeconv

import (
	"fmt"
	"strconv"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/compute/exprs/internal/shared"
	"github.com/substrait-io/substrait-go/v9/types"
)

// TimestampTzTimezone is the time zone of Arrow timestamps converted from
// Substrait timestamp_tz types.
const TimestampTzTimezone = "UTC"

// TypeAnchors resolves the extension anchors referenced by Substrait types.
type TypeAnchors interface {
	DecodeTypeArrow(anchor uint32) (arrow.DataType, bool)
	EncodeType(dt arrow.DataType) (uint32, bool)
	EncodeTypeVariation(dt arrow.DataType) (uint32, bool)
}

func isNullable(t types.Type) bool {
	return t.GetNullability() != types.NullabilityRequired
}

// FieldsFromSubstrait produces a list of arrow fields from a list of
// substrait types (such as the fields of a StructType) using nextName
// to determine the names for the fields.
func FieldsFromSubstrait(typeList []types.Type, nextName func() string, ext TypeAnchors) (out []arrow.Field, err error) {
	out = make([]arrow.Field, len(typeList))
	for i, t := range typeList {
		out[i].Name = nextName()
		out[i].Nullable = isNullable(t)

		if st, ok := t.(*types.StructType); ok {
			fields, err := FieldsFromSubstrait(st.Types, nextName, ext)
			if err != nil {
				return nil, err
			}
			out[i].Type = arrow.StructOf(fields...)
		} else {
			out[i].Type, _, err = FromSubstraitType(t, ext)
			if err != nil {
				return nil, err
			}
		}
	}
	return
}

// ToSubstraitType converts an arrow data type to a Substrait Type. Since
// arrow types don't have a nullable flag (it is in the arrow.Field) but
// Substrait types do, the nullability must be passed in here.
func ToSubstraitType(dt arrow.DataType, nullable bool, ext TypeAnchors) (types.Type, error) {
	var nullability types.Nullability
	if nullable {
		nullability = types.NullabilityNullable
	} else {
		nullability = types.NullabilityRequired
	}

	switch dt.ID() {
	case arrow.BOOL:
		return &types.BooleanType{Nullability: nullability}, nil
	case arrow.INT8:
		return &types.Int8Type{Nullability: nullability}, nil
	case arrow.INT16:
		return &types.Int16Type{Nullability: nullability}, nil
	case arrow.INT32:
		return &types.Int32Type{Nullability: nullability}, nil
	case arrow.INT64:
		return &types.Int64Type{Nullability: nullability}, nil
	case arrow.UINT8:
		anchor, ok := ext.EncodeTypeVariation(dt)
		if !ok {
			return nil, arrow.ErrNotFound
		}
		return &types.Int8Type{
			Nullability:      nullability,
			TypeVariationRef: anchor,
		}, nil
	case arrow.UINT16:
		anchor, ok := ext.EncodeTypeVariation(dt)
		if !ok {
			return nil, arrow.ErrNotFound
		}
		return &types.Int16Type{
			Nullability:      nullability,
			TypeVariationRef: anchor,
		}, nil
	case arrow.UINT32:
		anchor, ok := ext.EncodeTypeVariation(dt)
		if !ok {
			return nil, arrow.ErrNotFound
		}
		return &types.Int32Type{
			Nullability:      nullability,
			TypeVariationRef: anchor,
		}, nil
	case arrow.UINT64:
		anchor, ok := ext.EncodeTypeVariation(dt)
		if !ok {
			return nil, arrow.ErrNotFound
		}
		return &types.Int64Type{
			Nullability:      nullability,
			TypeVariationRef: anchor,
		}, nil
	case arrow.FLOAT16:
		anchor, ok := ext.EncodeTypeVariation(dt)
		if !ok {
			return nil, arrow.ErrNotFound
		}
		return &types.Int16Type{
			Nullability:      nullability,
			TypeVariationRef: anchor,
		}, nil
	case arrow.FLOAT32:
		return &types.Float32Type{Nullability: nullability}, nil
	case arrow.FLOAT64:
		return &types.Float64Type{Nullability: nullability}, nil
	case arrow.STRING, arrow.LARGE_STRING:
		return &types.StringType{Nullability: nullability}, nil
	case arrow.BINARY, arrow.LARGE_BINARY:
		return &types.BinaryType{Nullability: nullability}, nil
	case arrow.DATE32:
		return &types.DateType{Nullability: nullability}, nil
	case arrow.EXTENSION:
		dt := dt.(arrow.ExtensionType)
		switch dt.ExtensionName() {
		case "uuid":
			return &types.UUIDType{Nullability: nullability}, nil
		case "fixed_char":
			return &types.FixedCharType{
				Nullability: nullability,
				Length:      int32(dt.StorageType().(*arrow.FixedSizeBinaryType).ByteWidth),
			}, nil
		case "varchar":
			return &types.VarCharType{Nullability: nullability, Length: -1}, nil
		case "interval_year":
			return &types.IntervalYearType{Nullability: nullability}, nil
		case "interval_day":
			return &types.IntervalDayType{Nullability: nullability}, nil
		default:
			anchor, ok := ext.EncodeType(dt)
			if !ok {
				return nil, arrow.ErrNotFound
			}
			return &types.UserDefinedType{
				Nullability:   nullability,
				TypeReference: anchor,
			}, nil
		}
	case arrow.FIXED_SIZE_BINARY:
		return &types.FixedBinaryType{Nullability: nullability,
			Length: int32(dt.(*arrow.FixedSizeBinaryType).ByteWidth)}, nil
	case arrow.DECIMAL128, arrow.DECIMAL256:
		dt := dt.(arrow.DecimalType)
		return &types.DecimalType{Nullability: nullability,
			Precision: dt.GetPrecision(), Scale: dt.GetScale()}, nil
	case arrow.STRUCT:
		dt := dt.(*arrow.StructType)
		fields := make([]types.Type, dt.NumFields())
		var err error
		for i, f := range dt.Fields() {
			fields[i], err = ToSubstraitType(f.Type, f.Nullable, ext)
			if err != nil {
				return nil, err
			}
		}

		return &types.StructType{
			Nullability: nullability,
			Types:       fields,
		}, nil
	case arrow.LIST, arrow.FIXED_SIZE_LIST, arrow.LARGE_LIST:
		dt := dt.(arrow.NestedType)
		elemType, err := ToSubstraitType(dt.Fields()[0].Type, dt.Fields()[0].Nullable, ext)
		if err != nil {
			return nil, err
		}
		return &types.ListType{
			Nullability: nullability,
			Type:        elemType,
		}, nil
	case arrow.MAP:
		dt := dt.(*arrow.MapType)
		keyType, err := ToSubstraitType(dt.KeyType(), false, ext)
		if err != nil {
			return nil, err
		}
		valueType, err := ToSubstraitType(dt.ItemType(), dt.ItemField().Nullable, ext)
		if err != nil {
			return nil, err
		}

		return &types.MapType{
			Nullability: nullability,
			Key:         keyType,
			Value:       valueType,
		}, nil
	case arrow.TIME32:
		unit := dt.(*arrow.Time32Type).Unit
		return &types.PrecisionTimeType{
			Nullability: nullability,
			Precision:   types.TimePrecision(unit * 3),
		}, nil
	case arrow.TIME64:
		unit := dt.(*arrow.Time64Type).Unit
		return &types.PrecisionTimeType{
			Nullability: nullability,
			Precision:   types.TimePrecision(unit * 3),
		}, nil
	case arrow.TIMESTAMP:
		dt := dt.(*arrow.TimestampType)
		if dt.TimeZone != "" {
			return &types.PrecisionTimestampTzType{
				PrecisionTimestampType: types.PrecisionTimestampType{
					Nullability: nullability,
					Precision:   types.TimePrecision(dt.Unit * 3),
				},
			}, nil
		}
		return &types.PrecisionTimestampType{
			Nullability: nullability,
			Precision:   types.TimePrecision(dt.Unit * 3),
		}, nil
	}

	return nil, arrow.ErrNotImplemented
}

// FromSubstraitType returns the appropriate Arrow data type for the given
// substrait type, using the extension set if necessary.
// Since Substrait types contain their nullability also, the nullability
// returned along with the data type.
func FromSubstraitType(t types.Type, ext TypeAnchors) (arrow.DataType, bool, error) {
	nullable := isNullable(t)

	if t.GetTypeVariationReference() > 0 {
		dt, ok := ext.DecodeTypeArrow(t.GetTypeVariationReference())
		if ok {
			return dt, nullable, nil
		}
	}

	switch t := t.(type) {
	case *types.BooleanType:
		return arrow.FixedWidthTypes.Boolean, nullable, nil
	case *types.Int8Type:
		return arrow.PrimitiveTypes.Int8, nullable, nil
	case *types.Int16Type:
		return arrow.PrimitiveTypes.Int16, nullable, nil
	case *types.Int32Type:
		return arrow.PrimitiveTypes.Int32, nullable, nil
	case *types.Int64Type:
		return arrow.PrimitiveTypes.Int64, nullable, nil
	case *types.Float32Type:
		return arrow.PrimitiveTypes.Float32, nullable, nil
	case *types.Float64Type:
		return arrow.PrimitiveTypes.Float64, nullable, nil
	case *types.StringType:
		return arrow.BinaryTypes.String, nullable, nil
	case *types.BinaryType:
		return arrow.BinaryTypes.Binary, nullable, nil
	case *types.PrecisionTimeType:
		switch t.Precision {
		case types.PrecisionSeconds:
			return &arrow.Time32Type{Unit: arrow.Second}, nullable, nil
		case types.PrecisionMilliSeconds:
			return &arrow.Time32Type{Unit: arrow.Millisecond}, nullable, nil
		case types.PrecisionMicroSeconds:
			return &arrow.Time64Type{Unit: arrow.Microsecond}, nullable, nil
		case types.PrecisionNanoSeconds:
			return &arrow.Time64Type{Unit: arrow.Nanosecond}, nullable, nil
		}
	case *types.PrecisionTimestampType:
		switch t.Precision {
		case types.PrecisionSeconds:
			return &arrow.TimestampType{Unit: arrow.Second}, nullable, nil
		case types.PrecisionMilliSeconds:
			return &arrow.TimestampType{Unit: arrow.Millisecond}, nullable, nil
		case types.PrecisionMicroSeconds:
			return &arrow.TimestampType{Unit: arrow.Microsecond}, nullable, nil
		case types.PrecisionNanoSeconds:
			return &arrow.TimestampType{Unit: arrow.Nanosecond}, nullable, nil
		}
	case *types.PrecisionTimestampTzType:
		switch t.Precision {
		case types.PrecisionSeconds:
			return &arrow.TimestampType{Unit: arrow.Second, TimeZone: TimestampTzTimezone},
				nullable, nil
		case types.PrecisionMilliSeconds:
			return &arrow.TimestampType{Unit: arrow.Millisecond, TimeZone: TimestampTzTimezone},
				nullable, nil
		case types.PrecisionMicroSeconds:
			return &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: TimestampTzTimezone},
				nullable, nil
		case types.PrecisionNanoSeconds:
			return &arrow.TimestampType{Unit: arrow.Nanosecond, TimeZone: TimestampTzTimezone},
				nullable, nil
		}
	case *types.TimestampType:
		return &arrow.TimestampType{Unit: arrow.Microsecond}, nullable, nil
	case *types.TimestampTzType:
		return &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: TimestampTzTimezone},
			nullable, nil
	case *types.DateType:
		return arrow.FixedWidthTypes.Date32, nullable, nil
	case *types.TimeType:
		return &arrow.Time64Type{Unit: arrow.Microsecond}, nullable, nil
	case *types.IntervalYearType:
		return shared.IntervalYear(), nullable, nil
	case *types.IntervalDayType:
		return shared.IntervalDay(), nullable, nil
	case *types.UUIDType:
		return shared.UUID(), nullable, nil
	case *types.FixedCharType:
		return shared.FixedChar(t.Length), nullable, nil
	case *types.VarCharType:
		return shared.VarChar(t.Length), nullable, nil
	case *types.FixedBinaryType:
		return &arrow.FixedSizeBinaryType{ByteWidth: int(t.Length)}, nullable, nil
	case *types.DecimalType:
		return &arrow.Decimal128Type{
			Precision: t.Precision,
			Scale:     t.Scale,
		}, nullable, nil
	case *types.StructType:
		i := 0
		fields, err := FieldsFromSubstrait(t.Types, func() string {
			i++
			return strconv.Itoa(i)
		}, ext)
		if err != nil {
			return nil, false, err
		}

		return arrow.StructOf(fields...), nullable, nil
	case *types.ListType:
		elem, elemNullable, err := FromSubstraitType(t.Type, ext)
		if err != nil {
			return nil, false, err
		}
		return arrow.ListOfField(arrow.Field{Name: "item", Type: elem, Nullable: elemNullable}),
			nullable, nil
	case *types.MapType:
		key, keyNullable, err := FromSubstraitType(t.Key, ext)
		if err != nil {
			return nil, false, err
		}
		if keyNullable {
			return nil, false, fmt.Errorf("%w: encountered nullable key field when converting to arrow.Map",
				arrow.ErrInvalid)
		}

		value, valueNullable, err := FromSubstraitType(t.Value, ext)
		if err != nil {
			return nil, false, err
		}
		ret := arrow.MapOf(key, value)
		ret.SetItemNullable(valueNullable)
		return ret, nullable, nil
	case *types.UserDefinedType:
		anchor := t.TypeReference
		dt, ok := ext.DecodeTypeArrow(anchor)
		if !ok {
			return nil, false, arrow.ErrNotImplemented
		}
		return dt, nullable, nil
	}

	return nil, false, arrow.ErrNotImplemented
}
