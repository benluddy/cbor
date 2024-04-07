// Copyright (c) Faye Amacker. All rights reserved.
// Licensed under the MIT License. See LICENSE in the project root for license information.

package cbor

import (
	"encoding"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/x448/float16"
)

// Unmarshal parses the CBOR-encoded data into the value pointed to by v
// using default decoding options.  If v is nil, not a pointer, or
// a nil pointer, Unmarshal returns an error.
//
// To unmarshal CBOR into a value implementing the Unmarshaler interface,
// Unmarshal calls that value's UnmarshalCBOR method with a valid
// CBOR value.
//
// To unmarshal CBOR byte string into a value implementing the
// encoding.BinaryUnmarshaler interface, Unmarshal calls that value's
// UnmarshalBinary method with decoded CBOR byte string.
//
// To unmarshal CBOR into a pointer, Unmarshal sets the pointer to nil
// if CBOR data is null (0xf6) or undefined (0xf7).  Otherwise, Unmarshal
// unmarshals CBOR into the value pointed to by the pointer.  If the
// pointer is nil, Unmarshal creates a new value for it to point to.
//
// To unmarshal CBOR into an empty interface value, Unmarshal uses the
// following rules:
//
//	CBOR booleans decode to bool.
//	CBOR positive integers decode to uint64.
//	CBOR negative integers decode to int64 (big.Int if value overflows).
//	CBOR floating points decode to float64.
//	CBOR byte strings decode to []byte.
//	CBOR text strings decode to string.
//	CBOR arrays decode to []interface{}.
//	CBOR maps decode to map[interface{}]interface{}.
//	CBOR null and undefined values decode to nil.
//	CBOR times (tag 0 and 1) decode to time.Time.
//	CBOR bignums (tag 2 and 3) decode to big.Int.
//	CBOR tags with an unrecognized number decode to cbor.Tag
//
// To unmarshal a CBOR array into a slice, Unmarshal allocates a new slice
// if the CBOR array is empty or slice capacity is less than CBOR array length.
// Otherwise Unmarshal overwrites existing elements, and sets slice length
// to CBOR array length.
//
// To unmarshal a CBOR array into a Go array, Unmarshal decodes CBOR array
// elements into Go array elements.  If the Go array is smaller than the
// CBOR array, the extra CBOR array elements are discarded.  If the CBOR
// array is smaller than the Go array, the extra Go array elements are
// set to zero values.
//
// To unmarshal a CBOR array into a struct, struct must have a special field "_"
// with struct tag `cbor:",toarray"`.  Go array elements are decoded into struct
// fields.  Any "omitempty" struct field tag option is ignored in this case.
//
// To unmarshal a CBOR map into a map, Unmarshal allocates a new map only if the
// map is nil.  Otherwise Unmarshal reuses the existing map and keeps existing
// entries.  Unmarshal stores key-value pairs from the CBOR map into Go map.
// See DecOptions.DupMapKey to enable duplicate map key detection.
//
// To unmarshal a CBOR map into a struct, Unmarshal matches CBOR map keys to the
// keys in the following priority:
//
//  1. "cbor" key in struct field tag,
//  2. "json" key in struct field tag,
//  3. struct field name.
//
// Unmarshal tries an exact match for field name, then a case-insensitive match.
// Map key-value pairs without corresponding struct fields are ignored.  See
// DecOptions.ExtraReturnErrors to return error at unknown field.
//
// To unmarshal a CBOR text string into a time.Time value, Unmarshal parses text
// string formatted in RFC3339.  To unmarshal a CBOR integer/float into a
// time.Time value, Unmarshal creates an unix time with integer/float as seconds
// and fractional seconds since January 1, 1970 UTC. As a special case, Infinite
// and NaN float values decode to time.Time's zero value.
//
// To unmarshal CBOR null (0xf6) and undefined (0xf7) values into a
// slice/map/pointer, Unmarshal sets Go value to nil.  Because null is often
// used to mean "not present", unmarshalling CBOR null and undefined value
// into any other Go type has no effect and returns no error.
//
// Unmarshal supports CBOR tag 55799 (self-describe CBOR), tag 0 and 1 (time),
// and tag 2 and 3 (bignum).
//
// Unmarshal returns ExtraneousDataError error (without decoding into v)
// if there are any remaining bytes following the first valid CBOR data item.
// See UnmarshalFirst, if you want to unmarshal only the first
// CBOR data item without ExtraneousDataError caused by remaining bytes.
func Unmarshal(data []byte, v interface{}) error {
	return defaultDecMode.Unmarshal(data, v)
}

// UnmarshalFirst parses the first CBOR data item into the value pointed to by v
// using default decoding options.  Any remaining bytes are returned in rest.
//
// If v is nil, not a pointer, or a nil pointer, UnmarshalFirst returns an error.
//
// See the documentation for Unmarshal for details.
func UnmarshalFirst(data []byte, v interface{}) (rest []byte, err error) {
	return defaultDecMode.UnmarshalFirst(data, v)
}

// Valid checks whether data is a well-formed encoded CBOR data item and
// that it complies with default restrictions such as MaxNestedLevels,
// MaxArrayElements, MaxMapPairs, etc.
//
// If there are any remaining bytes after the CBOR data item,
// an ExtraneousDataError is returned.
//
// WARNING: Valid doesn't check if encoded CBOR data item is valid (i.e. validity)
// and RFC 8949 distinctly defines what is "Valid" and what is "Well-formed".
//
// Deprecated: Valid is kept for compatibility and should not be used.
// Use Wellformed instead because it has a more appropriate name.
func Valid(data []byte) error {
	return defaultDecMode.Valid(data)
}

// Wellformed checks whether data is a well-formed encoded CBOR data item and
// that it complies with default restrictions such as MaxNestedLevels,
// MaxArrayElements, MaxMapPairs, etc.
//
// If there are any remaining bytes after the CBOR data item,
// an ExtraneousDataError is returned.
func Wellformed(data []byte) error {
	return defaultDecMode.Wellformed(data)
}

// Unmarshaler is the interface implemented by types that wish to unmarshal
// CBOR data themselves.  The input is a valid CBOR value. UnmarshalCBOR
// must copy the CBOR data if it needs to use it after returning.
type Unmarshaler interface {
	UnmarshalCBOR([]byte) error
}

// InvalidUnmarshalError describes an invalid argument passed to Unmarshal.
type InvalidUnmarshalError struct {
	s string
}

func (e *InvalidUnmarshalError) Error() string {
	return e.s
}

// UnmarshalTypeError describes a CBOR value that can't be decoded to a Go type.
type UnmarshalTypeError struct {
	CBORType        string // type of CBOR value
	GoType          string // type of Go value it could not be decoded into
	StructFieldName string // name of the struct field holding the Go value (optional)
	errorMsg        string // additional error message (optional)
}

func (e *UnmarshalTypeError) Error() string {
	var s string
	if e.StructFieldName != "" {
		s = "cbor: cannot unmarshal " + e.CBORType + " into Go struct field " + e.StructFieldName + " of type " + e.GoType
	} else {
		s = "cbor: cannot unmarshal " + e.CBORType + " into Go value of type " + e.GoType
	}
	if e.errorMsg != "" {
		s += " (" + e.errorMsg + ")"
	}
	return s
}

// InvalidMapKeyTypeError describes invalid Go map key type when decoding CBOR map.
// For example, Go doesn't allow slice as map key.
type InvalidMapKeyTypeError struct {
	GoType string
}

func (e *InvalidMapKeyTypeError) Error() string {
	return "cbor: invalid map key type: " + e.GoType
}

// DupMapKeyError describes detected duplicate map key in CBOR map.
type DupMapKeyError struct {
	Key   interface{}
	Index int
}

func (e *DupMapKeyError) Error() string {
	return fmt.Sprintf("cbor: found duplicate map key \"%v\" at map element index %d", e.Key, e.Index)
}

// UnknownFieldError describes detected unknown field in CBOR map when decoding to Go struct.
type UnknownFieldError struct {
	Index int
}

func (e *UnknownFieldError) Error() string {
	return fmt.Sprintf("cbor: found unknown field at map element index %d", e.Index)
}

// UnacceptableDataItemError is returned when unmarshaling a CBOR input that contains a data item
// that is not acceptable to a specific CBOR-based application protocol ("invalid or unexpected" as
// described in RFC 8949 Section 5 Paragraph 3).
type UnacceptableDataItemError struct {
	CBORType string
	Message  string
}

func (e UnacceptableDataItemError) Error() string {
	return fmt.Sprintf("cbor: data item of cbor type %s is not accepted by protocol: %s", e.CBORType, e.Message)
}

// DupMapKeyMode specifies how to enforce duplicate map key. Two map keys are considered duplicates if:
//  1. When decoding into a struct, both keys match the same struct field. The keys are also
//     considered duplicates if neither matches any field and decoding to interface{} would produce
//     equal (==) values for both keys.
//  2. When decoding into a map, both keys are equal (==) when decoded into values of the
//     destination map's key type.
type DupMapKeyMode int

const (
	// DupMapKeyQuiet doesn't enforce duplicate map key. Decoder quietly (no error)
	// uses faster of "keep first" or "keep last" depending on Go data type and other factors.
	DupMapKeyQuiet DupMapKeyMode = iota

	// DupMapKeyEnforcedAPF enforces detection and rejection of duplicate map keys.
	// APF means "Allow Partial Fill" and the destination map or struct can be partially filled.
	// If a duplicate map key is detected, DupMapKeyError is returned without further decoding
	// of the map. It's the caller's responsibility to respond to DupMapKeyError by
	// discarding the partially filled result if their protocol requires it.
	// WARNING: using DupMapKeyEnforcedAPF will decrease performance and increase memory use.
	DupMapKeyEnforcedAPF

	maxDupMapKeyMode
)

func (dmkm DupMapKeyMode) valid() bool {
	return dmkm >= 0 && dmkm < maxDupMapKeyMode
}

// IndefLengthMode specifies whether to allow indefinite length items.
type IndefLengthMode int

const (
	// IndefLengthAllowed allows indefinite length items.
	IndefLengthAllowed IndefLengthMode = iota

	// IndefLengthForbidden disallows indefinite length items.
	IndefLengthForbidden

	maxIndefLengthMode
)

func (m IndefLengthMode) valid() bool {
	return m >= 0 && m < maxIndefLengthMode
}

// TagsMode specifies whether to allow CBOR tags.
type TagsMode int

const (
	// TagsAllowed allows CBOR tags.
	TagsAllowed TagsMode = iota

	// TagsForbidden disallows CBOR tags.
	TagsForbidden

	maxTagsMode
)

func (tm TagsMode) valid() bool {
	return tm >= 0 && tm < maxTagsMode
}

// IntDecMode specifies which Go type (int64, uint64, or big.Int) should
// be used when decoding CBOR integers (major type 0 and 1) to Go interface{}.
type IntDecMode int

const (
	// IntDecConvertNone affects how CBOR integers (major type 0 and 1) decode to Go interface{}.
	// It decodes CBOR unsigned integer (major type 0) to:
	// - uint64
	// It decodes CBOR negative integer (major type 1) to:
	// - int64 if value fits
	// - big.Int or *big.Int (see BigIntDecMode) if value doesn't fit into int64
	IntDecConvertNone IntDecMode = iota

	// IntDecConvertSigned affects how CBOR integers (major type 0 and 1) decode to Go interface{}.
	// It decodes CBOR integers (major type 0 and 1) to:
	// - int64 if value fits
	// - big.Int or *big.Int (see BigIntDecMode) if value < math.MinInt64
	// - return UnmarshalTypeError if value > math.MaxInt64
	// Deprecated: IntDecConvertSigned should not be used.
	// Please use other options, such as IntDecConvertSignedOrError, IntDecConvertSignedOrBigInt, IntDecConvertNone.
	IntDecConvertSigned

	// IntDecConvertSignedOrFail affects how CBOR integers (major type 0 and 1) decode to Go interface{}.
	// It decodes CBOR integers (major type 0 and 1) to:
	// - int64 if value fits
	// - return UnmarshalTypeError if value doesn't fit into int64
	IntDecConvertSignedOrFail

	// IntDecConvertSigned affects how CBOR integers (major type 0 and 1) decode to Go interface{}.
	// It makes CBOR integers (major type 0 and 1) decode to:
	// - int64 if value fits
	// - big.Int or *big.Int (see BigIntDecMode) if value doesn't fit into int64
	IntDecConvertSignedOrBigInt

	maxIntDec
)

func (idm IntDecMode) valid() bool {
	return idm >= 0 && idm < maxIntDec
}

// MapKeyByteStringMode specifies how to decode CBOR byte string (major type 2)
// as Go map key when decoding CBOR map key into an empty Go interface value.
// Specifically, this option applies when decoding CBOR map into
// - Go empty interface, or
// - Go map with empty interface as key type.
// The CBOR map key types handled by this option are
// - byte string
// - tagged byte string
// - nested tagged byte string
type MapKeyByteStringMode int

const (
	// MapKeyByteStringAllowed allows CBOR byte string to be decoded as Go map key.
	// Since Go doesn't allow []byte as map key, CBOR byte string is decoded to
	// ByteString which has underlying string type.
	// This is the default setting.
	MapKeyByteStringAllowed MapKeyByteStringMode = iota

	// MapKeyByteStringForbidden forbids CBOR byte string being decoded as Go map key.
	// Attempting to decode CBOR byte string as map key into empty interface value
	// returns a decoding error.
	MapKeyByteStringForbidden

	maxMapKeyByteStringMode
)

func (mkbsm MapKeyByteStringMode) valid() bool {
	return mkbsm >= 0 && mkbsm < maxMapKeyByteStringMode
}

// ExtraDecErrorCond specifies extra conditions that should be treated as errors.
type ExtraDecErrorCond uint

// ExtraDecErrorNone indicates no extra error condition.
const ExtraDecErrorNone ExtraDecErrorCond = 0

const (
	// ExtraDecErrorUnknownField indicates error condition when destination
	// Go struct doesn't have a field matching a CBOR map key.
	ExtraDecErrorUnknownField ExtraDecErrorCond = 1 << iota

	maxExtraDecError
)

func (ec ExtraDecErrorCond) valid() bool {
	return ec < maxExtraDecError
}

// UTF8Mode option specifies if decoder should
// decode CBOR Text containing invalid UTF-8 string.
type UTF8Mode int

const (
	// UTF8RejectInvalid rejects CBOR Text containing
	// invalid UTF-8 string.
	UTF8RejectInvalid UTF8Mode = iota

	// UTF8DecodeInvalid allows decoding CBOR Text containing
	// invalid UTF-8 string.
	UTF8DecodeInvalid

	maxUTF8Mode
)

func (um UTF8Mode) valid() bool {
	return um >= 0 && um < maxUTF8Mode
}

// FieldNameMatchingMode specifies how string keys in CBOR maps are matched to Go struct field names.
type FieldNameMatchingMode int

const (
	// FieldNameMatchingPreferCaseSensitive prefers to decode map items into struct fields whose names (or tag
	// names) exactly match the item's key. If there is no such field, a map item will be decoded into a field whose
	// name is a case-insensitive match for the item's key.
	FieldNameMatchingPreferCaseSensitive FieldNameMatchingMode = iota

	// FieldNameMatchingCaseSensitive decodes map items only into a struct field whose name (or tag name) is an
	// exact match for the item's key.
	FieldNameMatchingCaseSensitive

	maxFieldNameMatchingMode
)

func (fnmm FieldNameMatchingMode) valid() bool {
	return fnmm >= 0 && fnmm < maxFieldNameMatchingMode
}

// BigIntDecMode specifies how to decode CBOR bignum to Go interface{}.
type BigIntDecMode int

const (
	// BigIntDecodeValue makes CBOR bignum decode to big.Int (instead of *big.Int)
	// when unmarshalling into a Go interface{}.
	BigIntDecodeValue BigIntDecMode = iota

	// BigIntDecodePointer makes CBOR bignum decode to *big.Int when
	// unmarshalling into a Go interface{}.
	BigIntDecodePointer

	maxBigIntDecMode
)

func (bidm BigIntDecMode) valid() bool {
	return bidm >= 0 && bidm < maxBigIntDecMode
}

// ByteStringToStringMode specifies the behavior when decoding a CBOR byte string into a Go string.
type ByteStringToStringMode int

const (
	// ByteStringToStringForbidden generates an error on an attempt to decode a CBOR byte string into a Go string.
	ByteStringToStringForbidden ByteStringToStringMode = iota

	// ByteStringToStringAllowed permits decoding a CBOR byte string into a Go string.
	ByteStringToStringAllowed

	maxByteStringToStringMode
)

func (bstsm ByteStringToStringMode) valid() bool {
	return bstsm >= 0 && bstsm < maxByteStringToStringMode
}

// FieldNameByteStringMode specifies the behavior when decoding a CBOR byte string map key as a Go struct field name.
type FieldNameByteStringMode int

const (
	// FieldNameByteStringForbidden generates an error on an attempt to decode a CBOR byte string map key as a Go struct field name.
	FieldNameByteStringForbidden FieldNameByteStringMode = iota

	// FieldNameByteStringAllowed permits CBOR byte string map keys to be recognized as Go struct field names.
	FieldNameByteStringAllowed

	maxFieldNameByteStringMode
)

func (fnbsm FieldNameByteStringMode) valid() bool {
	return fnbsm >= 0 && fnbsm < maxFieldNameByteStringMode
}

// UnrecognizedTagToAnyMode specifies how to decode unrecognized CBOR tag into an empty interface (any).
// Currently, recognized CBOR tag numbers are 0, 1, 2, 3, or registered by TagSet.
type UnrecognizedTagToAnyMode int

const (
	// UnrecognizedTagNumAndContentToAny decodes CBOR tag number and tag content to cbor.Tag
	// when decoding unrecognized CBOR tag into an empty interface.
	UnrecognizedTagNumAndContentToAny UnrecognizedTagToAnyMode = iota

	// UnrecognizedTagContentToAny decodes only CBOR tag content (into its default type)
	// when decoding unrecognized CBOR tag into an empty interface.
	UnrecognizedTagContentToAny

	maxUnrecognizedTagToAny
)

func (uttam UnrecognizedTagToAnyMode) valid() bool {
	return uttam >= 0 && uttam < maxUnrecognizedTagToAny
}

// TimeTagToAnyMode specifies how to decode CBOR tag 0 and 1 into an empty interface (any).
// Based on the specified mode, Unmarshal can return a time.Time value or a time string in a specific format.
type TimeTagToAnyMode int

const (
	// TimeTagToTime decodes CBOR tag 0 and 1 into a time.Time value
	// when decoding tag 0 or 1 into an empty interface.
	TimeTagToTime TimeTagToAnyMode = iota

	// TimeTagToRFC3339 decodes CBOR tag 0 and 1 into a time string in RFC3339 format
	// when decoding tag 0 or 1 into an empty interface.
	TimeTagToRFC3339

	// TimeTagToRFC3339Nano decodes CBOR tag 0 and 1 into a time string in RFC3339Nano format
	// when decoding tag 0 or 1 into an empty interface.
	TimeTagToRFC3339Nano

	maxTimeTagToAnyMode
)

func (tttam TimeTagToAnyMode) valid() bool {
	return tttam >= 0 && tttam < maxTimeTagToAnyMode
}

// SimpleValueRegistry is a registry of unmarshaling behaviors for each possible CBOR simple value
// number (0...23 and 32...255).
type SimpleValueRegistry struct {
	rejected [256]bool
}

// WithRejectedSimpleValue registers the given simple value as rejected. If the simple value is
// encountered in a CBOR input during unmarshaling, an UnacceptableDataItemError is returned.
func WithRejectedSimpleValue(sv SimpleValue) func(*SimpleValueRegistry) error {
	return func(r *SimpleValueRegistry) error {
		if sv >= 24 && sv <= 31 {
			return fmt.Errorf("cbor: cannot set analog for reserved simple value %d", sv)
		}
		r.rejected[sv] = true
		return nil
	}
}

// Creates a new SimpleValueRegistry. The registry state is initialized by executing the provided
// functions in order against a registry that is pre-populated with the defaults for all well-formed
// simple value numbers.
func NewSimpleValueRegistryFromDefaults(fns ...func(*SimpleValueRegistry) error) (*SimpleValueRegistry, error) {
	var r SimpleValueRegistry
	for _, fn := range fns {
		if err := fn(&r); err != nil {
			return nil, err
		}
	}
	return &r, nil
}

// DecOptions specifies decoding options.
type DecOptions struct {
	// DupMapKey specifies whether to enforce duplicate map key.
	DupMapKey DupMapKeyMode

	// TimeTag specifies whether or not untagged data items, or tags other
	// than tag 0 and tag 1, can be decoded to time.Time. If tag 0 or tag 1
	// appears in an input, the type of its content is always validated as
	// specified in RFC 8949. That behavior is not controlled by this
	// option. The behavior of the supported modes are:
	//
	//   DecTagIgnored (default): Untagged text strings and text strings
	//   enclosed in tags other than 0 and 1 are decoded as though enclosed
	//   in tag 0. Untagged unsigned integers, negative integers, and
	//   floating-point numbers (or those enclosed in tags other than 0 and
	//   1) are decoded as though enclosed in tag 1. Decoding a tag other
	//   than 0 or 1 enclosing simple values null or undefined into a
	//   time.Time does not modify the destination value.
	//
	//   DecTagOptional: Untagged text strings are decoded as though
	//   enclosed in tag 0. Untagged unsigned integers, negative integers,
	//   and floating-point numbers are decoded as though enclosed in tag
	//   1. Tags other than 0 and 1 will produce an error on attempts to
	//   decode them into a time.Time.
	//
	//   DecTagRequired: Only tags 0 and 1 can be decoded to time.Time. Any
	//   other input will produce an error.
	TimeTag DecTagMode

	// MaxNestedLevels specifies the max nested levels allowed for any combination of CBOR array, maps, and tags.
	// Default is 32 levels and it can be set to [4, 65535]. Note that higher maximum levels of nesting can
	// require larger amounts of stack to deserialize. Don't increase this higher than you require.
	MaxNestedLevels int

	// MaxArrayElements specifies the max number of elements for CBOR arrays.
	// Default is 128*1024=131072 and it can be set to [16, 2147483647]
	MaxArrayElements int

	// MaxMapPairs specifies the max number of key-value pairs for CBOR maps.
	// Default is 128*1024=131072 and it can be set to [16, 2147483647]
	MaxMapPairs int

	// IndefLength specifies whether to allow indefinite length CBOR items.
	IndefLength IndefLengthMode

	// TagsMd specifies whether to allow CBOR tags (major type 6).
	TagsMd TagsMode

	// IntDec specifies which Go integer type (int64 or uint64) to use
	// when decoding CBOR int (major type 0 and 1) to Go interface{}.
	IntDec IntDecMode

	// MapKeyByteString specifies how to decode CBOR byte string as map key
	// when decoding CBOR map with byte string key into an empty interface value.
	// By default, an error is returned when attempting to decode CBOR byte string
	// as map key because Go doesn't allow []byte as map key.
	MapKeyByteString MapKeyByteStringMode

	// ExtraReturnErrors specifies extra conditions that should be treated as errors.
	ExtraReturnErrors ExtraDecErrorCond

	// DefaultMapType specifies Go map type to create and decode to
	// when unmarshalling CBOR into an empty interface value.
	// By default, unmarshal uses map[interface{}]interface{}.
	DefaultMapType reflect.Type

	// UTF8 specifies if decoder should decode CBOR Text containing invalid UTF-8.
	// By default, unmarshal rejects CBOR text containing invalid UTF-8.
	UTF8 UTF8Mode

	// FieldNameMatching specifies how string keys in CBOR maps are matched to Go struct field names.
	FieldNameMatching FieldNameMatchingMode

	// BigIntDec specifies how to decode CBOR bignum to Go interface{}.
	BigIntDec BigIntDecMode

	// DefaultByteStringType is the Go type that should be produced when decoding a CBOR byte
	// string into an empty interface value. Types to which a []byte is convertible are valid
	// for this option, except for array and pointer-to-array types. If nil, the default is
	// []byte.
	DefaultByteStringType reflect.Type

	// ByteStringToString specifies the behavior when decoding a CBOR byte string into a Go string.
	ByteStringToString ByteStringToStringMode

	// FieldNameByteString specifies the behavior when decoding a CBOR byte string map key as a
	// Go struct field name.
	FieldNameByteString FieldNameByteStringMode

	// UnrecognizedTagToAny specifies how to decode unrecognized CBOR tag into an empty interface.
	// Currently, recognized CBOR tag numbers are 0, 1, 2, 3, or registered by TagSet.
	UnrecognizedTagToAny UnrecognizedTagToAnyMode

	// TimeTagToAnyMode specifies how to decode CBOR tag 0 and 1 into an empty interface (any).
	// Based on the specified mode, Unmarshal can return a time.Time value or a time string in a specific format.
	TimeTagToAny TimeTagToAnyMode

	// SimpleValues is an immutable mapping from each CBOR simple value to a corresponding
	// unmarshal behavior. If nil, the simple values false, true, null, and undefined are mapped
	// to the Go analog values false, true, nil, and nil, respectively, and all other simple
	// values N (except the reserved simple values 24 through 31) are mapped to
	// cbor.SimpleValue(N). In other words, all well-formed simple values can be decoded.
	//
	// Users may provide a custom SimpleValueRegistry constructed via
	// NewSimpleValueRegistryFromDefaults.
	SimpleValues *SimpleValueRegistry
}

// DecMode returns DecMode with immutable options and no tags (safe for concurrency).
func (opts DecOptions) DecMode() (DecMode, error) {
	return opts.decMode()
}

// DecModeWithTags returns DecMode with options and tags that are both immutable (safe for concurrency).
func (opts DecOptions) DecModeWithTags(tags TagSet) (DecMode, error) {
	if opts.TagsMd == TagsForbidden {
		return nil, errors.New("cbor: cannot create DecMode with TagSet when TagsMd is TagsForbidden")
	}
	if tags == nil {
		return nil, errors.New("cbor: cannot create DecMode with nil value as TagSet")
	}

	dm, err := opts.decMode()
	if err != nil {
		return nil, err
	}

	// Copy tags
	ts := tagSet(make(map[reflect.Type]*tagItem))
	syncTags := tags.(*syncTagSet)
	syncTags.RLock()
	for contentType, tag := range syncTags.t {
		if tag.opts.DecTag != DecTagIgnored {
			ts[contentType] = tag
		}
	}
	syncTags.RUnlock()

	if len(ts) > 0 {
		dm.tags = ts
	}

	return dm, nil
}

// DecModeWithSharedTags returns DecMode with immutable options and mutable shared tags (safe for concurrency).
func (opts DecOptions) DecModeWithSharedTags(tags TagSet) (DecMode, error) {
	if opts.TagsMd == TagsForbidden {
		return nil, errors.New("cbor: cannot create DecMode with TagSet when TagsMd is TagsForbidden")
	}
	if tags == nil {
		return nil, errors.New("cbor: cannot create DecMode with nil value as TagSet")
	}
	dm, err := opts.decMode()
	if err != nil {
		return nil, err
	}
	dm.tags = tags
	return dm, nil
}

const (
	defaultMaxArrayElements = 131072
	minMaxArrayElements     = 16
	maxMaxArrayElements     = 2147483647

	defaultMaxMapPairs = 131072
	minMaxMapPairs     = 16
	maxMaxMapPairs     = 2147483647

	defaultMaxNestedLevels = 32
	minMaxNestedLevels     = 4
	maxMaxNestedLevels     = 65535
)

var defaultSimpleValues = func() *SimpleValueRegistry {
	registry, err := NewSimpleValueRegistryFromDefaults()
	if err != nil {
		panic(err)
	}
	return registry
}()

//nolint:gocyclo // Each option comes with some manageable boilerplate
func (opts DecOptions) decMode() (*decMode, error) {
	if !opts.DupMapKey.valid() {
		return nil, errors.New("cbor: invalid DupMapKey " + strconv.Itoa(int(opts.DupMapKey)))
	}

	if !opts.TimeTag.valid() {
		return nil, errors.New("cbor: invalid TimeTag " + strconv.Itoa(int(opts.TimeTag)))
	}

	if !opts.IndefLength.valid() {
		return nil, errors.New("cbor: invalid IndefLength " + strconv.Itoa(int(opts.IndefLength)))
	}

	if !opts.TagsMd.valid() {
		return nil, errors.New("cbor: invalid TagsMd " + strconv.Itoa(int(opts.TagsMd)))
	}

	if !opts.IntDec.valid() {
		return nil, errors.New("cbor: invalid IntDec " + strconv.Itoa(int(opts.IntDec)))
	}

	if !opts.MapKeyByteString.valid() {
		return nil, errors.New("cbor: invalid MapKeyByteString " + strconv.Itoa(int(opts.MapKeyByteString)))
	}

	if opts.MaxNestedLevels == 0 {
		opts.MaxNestedLevels = defaultMaxNestedLevels
	} else if opts.MaxNestedLevels < minMaxNestedLevels || opts.MaxNestedLevels > maxMaxNestedLevels {
		return nil, errors.New("cbor: invalid MaxNestedLevels " + strconv.Itoa(opts.MaxNestedLevels) +
			" (range is [" + strconv.Itoa(minMaxNestedLevels) + ", " + strconv.Itoa(maxMaxNestedLevels) + "])")
	}

	if opts.MaxArrayElements == 0 {
		opts.MaxArrayElements = defaultMaxArrayElements
	} else if opts.MaxArrayElements < minMaxArrayElements || opts.MaxArrayElements > maxMaxArrayElements {
		return nil, errors.New("cbor: invalid MaxArrayElements " + strconv.Itoa(opts.MaxArrayElements) +
			" (range is [" + strconv.Itoa(minMaxArrayElements) + ", " + strconv.Itoa(maxMaxArrayElements) + "])")
	}

	if opts.MaxMapPairs == 0 {
		opts.MaxMapPairs = defaultMaxMapPairs
	} else if opts.MaxMapPairs < minMaxMapPairs || opts.MaxMapPairs > maxMaxMapPairs {
		return nil, errors.New("cbor: invalid MaxMapPairs " + strconv.Itoa(opts.MaxMapPairs) +
			" (range is [" + strconv.Itoa(minMaxMapPairs) + ", " + strconv.Itoa(maxMaxMapPairs) + "])")
	}

	if !opts.ExtraReturnErrors.valid() {
		return nil, errors.New("cbor: invalid ExtraReturnErrors " + strconv.Itoa(int(opts.ExtraReturnErrors)))
	}

	if opts.DefaultMapType != nil && opts.DefaultMapType.Kind() != reflect.Map {
		return nil, fmt.Errorf("cbor: invalid DefaultMapType %s", opts.DefaultMapType)
	}

	if !opts.UTF8.valid() {
		return nil, errors.New("cbor: invalid UTF8 " + strconv.Itoa(int(opts.UTF8)))
	}

	if !opts.FieldNameMatching.valid() {
		return nil, errors.New("cbor: invalid FieldNameMatching " + strconv.Itoa(int(opts.FieldNameMatching)))
	}

	if !opts.BigIntDec.valid() {
		return nil, errors.New("cbor: invalid BigIntDec " + strconv.Itoa(int(opts.BigIntDec)))
	}

	if opts.DefaultByteStringType != nil &&
		opts.DefaultByteStringType.Kind() != reflect.String &&
		(opts.DefaultByteStringType.Kind() != reflect.Slice || opts.DefaultByteStringType.Elem().Kind() != reflect.Uint8) {
		return nil, fmt.Errorf("cbor: invalid DefaultByteStringType: %s is not of kind string or []uint8", opts.DefaultByteStringType)
	}

	if !opts.ByteStringToString.valid() {
		return nil, errors.New("cbor: invalid ByteStringToString " + strconv.Itoa(int(opts.ByteStringToString)))
	}

	if !opts.FieldNameByteString.valid() {
		return nil, errors.New("cbor: invalid FieldNameByteString " + strconv.Itoa(int(opts.FieldNameByteString)))
	}

	if !opts.UnrecognizedTagToAny.valid() {
		return nil, errors.New("cbor: invalid UnrecognizedTagToAnyMode " + strconv.Itoa(int(opts.UnrecognizedTagToAny)))
	}
	simpleValues := opts.SimpleValues
	if simpleValues == nil {
		simpleValues = defaultSimpleValues
	}

	if !opts.TimeTagToAny.valid() {
		return nil, errors.New("cbor: invalid TimeTagToAny " + strconv.Itoa(int(opts.TimeTagToAny)))
	}

	dm := decMode{
		dupMapKey:             opts.DupMapKey,
		timeTag:               opts.TimeTag,
		maxNestedLevels:       opts.MaxNestedLevels,
		maxArrayElements:      opts.MaxArrayElements,
		maxMapPairs:           opts.MaxMapPairs,
		indefLength:           opts.IndefLength,
		tagsMd:                opts.TagsMd,
		intDec:                opts.IntDec,
		mapKeyByteString:      opts.MapKeyByteString,
		extraReturnErrors:     opts.ExtraReturnErrors,
		defaultMapType:        opts.DefaultMapType,
		utf8:                  opts.UTF8,
		fieldNameMatching:     opts.FieldNameMatching,
		bigIntDec:             opts.BigIntDec,
		defaultByteStringType: opts.DefaultByteStringType,
		byteStringToString:    opts.ByteStringToString,
		fieldNameByteString:   opts.FieldNameByteString,
		unrecognizedTagToAny:  opts.UnrecognizedTagToAny,
		timeTagToAny:          opts.TimeTagToAny,
		simpleValues:          simpleValues,
	}

	return &dm, nil
}

// DecMode is the main interface for CBOR decoding.
type DecMode interface {
	// Unmarshal parses the CBOR-encoded data into the value pointed to by v
	// using the decoding mode.  If v is nil, not a pointer, or a nil pointer,
	// Unmarshal returns an error.
	//
	// See the documentation for Unmarshal for details.
	Unmarshal(data []byte, v interface{}) error

	// UnmarshalFirst parses the first CBOR data item into the value pointed to by v
	// using the decoding mode.  Any remaining bytes are returned in rest.
	//
	// If v is nil, not a pointer, or a nil pointer, UnmarshalFirst returns an error.
	//
	// See the documentation for Unmarshal for details.
	UnmarshalFirst(data []byte, v interface{}) (rest []byte, err error)

	// Valid checks whether data is a well-formed encoded CBOR data item and
	// that it complies with configurable restrictions such as MaxNestedLevels,
	// MaxArrayElements, MaxMapPairs, etc.
	//
	// If there are any remaining bytes after the CBOR data item,
	// an ExtraneousDataError is returned.
	//
	// WARNING: Valid doesn't check if encoded CBOR data item is valid (i.e. validity)
	// and RFC 8949 distinctly defines what is "Valid" and what is "Well-formed".
	//
	// Deprecated: Valid is kept for compatibility and should not be used.
	// Use Wellformed instead because it has a more appropriate name.
	Valid(data []byte) error

	// Wellformed checks whether data is a well-formed encoded CBOR data item and
	// that it complies with configurable restrictions such as MaxNestedLevels,
	// MaxArrayElements, MaxMapPairs, etc.
	//
	// If there are any remaining bytes after the CBOR data item,
	// an ExtraneousDataError is returned.
	Wellformed(data []byte) error

	// NewDecoder returns a new decoder that reads from r using dm DecMode.
	NewDecoder(r io.Reader) *Decoder

	// DecOptions returns user specified options used to create this DecMode.
	DecOptions() DecOptions
}

type decMode struct {
	tags                  tagProvider
	dupMapKey             DupMapKeyMode
	timeTag               DecTagMode
	maxNestedLevels       int
	maxArrayElements      int
	maxMapPairs           int
	indefLength           IndefLengthMode
	tagsMd                TagsMode
	intDec                IntDecMode
	mapKeyByteString      MapKeyByteStringMode
	extraReturnErrors     ExtraDecErrorCond
	defaultMapType        reflect.Type
	utf8                  UTF8Mode
	fieldNameMatching     FieldNameMatchingMode
	bigIntDec             BigIntDecMode
	defaultByteStringType reflect.Type
	byteStringToString    ByteStringToStringMode
	fieldNameByteString   FieldNameByteStringMode
	unrecognizedTagToAny  UnrecognizedTagToAnyMode
	timeTagToAny          TimeTagToAnyMode
	simpleValues          *SimpleValueRegistry
}

var defaultDecMode, _ = DecOptions{}.decMode()

// DecOptions returns user specified options used to create this DecMode.
func (dm *decMode) DecOptions() DecOptions {
	simpleValues := dm.simpleValues
	if simpleValues == defaultSimpleValues {
		// Users can't explicitly set this to defaultSimpleValues. It must have been nil in
		// the original DecOptions.
		simpleValues = nil
	}

	return DecOptions{
		DupMapKey:             dm.dupMapKey,
		TimeTag:               dm.timeTag,
		MaxNestedLevels:       dm.maxNestedLevels,
		MaxArrayElements:      dm.maxArrayElements,
		MaxMapPairs:           dm.maxMapPairs,
		IndefLength:           dm.indefLength,
		TagsMd:                dm.tagsMd,
		IntDec:                dm.intDec,
		MapKeyByteString:      dm.mapKeyByteString,
		ExtraReturnErrors:     dm.extraReturnErrors,
		DefaultMapType:        dm.defaultMapType,
		UTF8:                  dm.utf8,
		FieldNameMatching:     dm.fieldNameMatching,
		BigIntDec:             dm.bigIntDec,
		DefaultByteStringType: dm.defaultByteStringType,
		ByteStringToString:    dm.byteStringToString,
		FieldNameByteString:   dm.fieldNameByteString,
		UnrecognizedTagToAny:  dm.unrecognizedTagToAny,
		TimeTagToAny:          dm.timeTagToAny,
		SimpleValues:          simpleValues,
	}
}

// Unmarshal parses the CBOR-encoded data into the value pointed to by v
// using dm decoding mode.  If v is nil, not a pointer, or a nil pointer,
// Unmarshal returns an error.
//
// See the documentation for Unmarshal for details.
func (dm *decMode) Unmarshal(data []byte, v interface{}) error {
	d := decoder{data: data, dm: dm}

	// Check well-formedness.
	off := d.off                      // Save offset before data validation
	err := d.wellformed(false, false) // don't allow any extra data after valid data item.
	d.off = off                       // Restore offset
	if err != nil {
		return err
	}

	return d.value(v)
}

// UnmarshalFirst parses the first CBOR data item into the value pointed to by v
// using dm decoding mode.  Any remaining bytes are returned in rest.
//
// If v is nil, not a pointer, or a nil pointer, UnmarshalFirst returns an error.
//
// See the documentation for Unmarshal for details.
func (dm *decMode) UnmarshalFirst(data []byte, v interface{}) (rest []byte, err error) {
	d := decoder{data: data, dm: dm}

	// check well-formedness.
	off := d.off                    // Save offset before data validation
	err = d.wellformed(true, false) // allow extra data after well-formed data item
	d.off = off                     // Restore offset

	// If it is well-formed, parse the value. This is structured like this to allow
	// better test coverage
	if err == nil {
		err = d.value(v)
	}

	// If either wellformed or value returned an error, do not return rest bytes
	if err != nil {
		return nil, err
	}

	// Return the rest of the data slice (which might be len 0)
	return d.data[d.off:], nil
}

// Valid checks whether data is a well-formed encoded CBOR data item and
// that it complies with configurable restrictions such as MaxNestedLevels,
// MaxArrayElements, MaxMapPairs, etc.
//
// If there are any remaining bytes after the CBOR data item,
// an ExtraneousDataError is returned.
//
// WARNING: Valid doesn't check if encoded CBOR data item is valid (i.e. validity)
// and RFC 8949 distinctly defines what is "Valid" and what is "Well-formed".
//
// Deprecated: Valid is kept for compatibility and should not be used.
// Use Wellformed instead because it has a more appropriate name.
func (dm *decMode) Valid(data []byte) error {
	return dm.Wellformed(data)
}

// Wellformed checks whether data is a well-formed encoded CBOR data item and
// that it complies with configurable restrictions such as MaxNestedLevels,
// MaxArrayElements, MaxMapPairs, etc.
//
// If there are any remaining bytes after the CBOR data item,
// an ExtraneousDataError is returned.
func (dm *decMode) Wellformed(data []byte) error {
	d := decoder{data: data, dm: dm}
	return d.wellformed(false, false)
}

// NewDecoder returns a new decoder that reads from r using dm DecMode.
func (dm *decMode) NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: r, d: decoder{dm: dm}}
}

type decoder struct {
	data []byte
	off  int // next read offset in data
	dm   *decMode
}

// value decodes CBOR data item into the value pointed to by v.
// If CBOR data item fails to be decoded into v,
// error is returned and offset is moved to the next CBOR data item.
// Precondition: d.data contains at least one well-formed CBOR data item.
func (d *decoder) value(v interface{}) error {
	// v can't be nil, non-pointer, or nil pointer value.
	if v == nil {
		return &InvalidUnmarshalError{"cbor: Unmarshal(nil)"}
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr {
		return &InvalidUnmarshalError{"cbor: Unmarshal(non-pointer " + rv.Type().String() + ")"}
	} else if rv.IsNil() {
		return &InvalidUnmarshalError{"cbor: Unmarshal(nil " + rv.Type().String() + ")"}
	}
	rv = rv.Elem()
	return d.parseToValue(rv, getTypeInfo(rv.Type()))
}

type cborType uint8

const (
	cborTypePositiveInt cborType = 0x00
	cborTypeNegativeInt cborType = 0x20
	cborTypeByteString  cborType = 0x40
	cborTypeTextString  cborType = 0x60
	cborTypeArray       cborType = 0x80
	cborTypeMap         cborType = 0xa0
	cborTypeTag         cborType = 0xc0
	cborTypePrimitives  cborType = 0xe0
)

func (t cborType) String() string {
	switch t {
	case cborTypePositiveInt:
		return "positive integer"
	case cborTypeNegativeInt:
		return "negative integer"
	case cborTypeByteString:
		return "byte string"
	case cborTypeTextString:
		return "UTF-8 text string"
	case cborTypeArray:
		return "array"
	case cborTypeMap:
		return "map"
	case cborTypeTag:
		return "tag"
	case cborTypePrimitives:
		return "primitives"
	default:
		return "Invalid type " + strconv.Itoa(int(t))
	}
}

const (
	selfDescribedCBORTagNum = 55799
)

// parseToValue decodes CBOR data to value.  It assumes data is well-formed,
// and does not perform bounds checking.
func (d *decoder) parseToValue(v reflect.Value, tInfo *typeInfo) error { //nolint:gocyclo

	// Decode CBOR nil or CBOR undefined to pointer value by setting pointer value to nil.
	if d.nextCBORNil() && v.Kind() == reflect.Ptr {
		d.skip()
		v.Set(reflect.Zero(v.Type()))
		return nil
	}

	if tInfo.spclType == specialTypeIface {
		if !v.IsNil() {
			// Use value type
			v = v.Elem()
			tInfo = getTypeInfo(v.Type())
		} else {
			// Create and use registered type if CBOR data is registered tag
			if d.dm.tags != nil && d.nextCBORType() == cborTypeTag {

				off := d.off
				var tagNums []uint64
				for d.nextCBORType() == cborTypeTag {
					_, _, tagNum := d.getHead()
					tagNums = append(tagNums, tagNum)
				}
				d.off = off

				registeredType := d.dm.tags.getTypeFromTagNum(tagNums)
				if registeredType != nil {
					if registeredType.Implements(tInfo.nonPtrType) ||
						reflect.PtrTo(registeredType).Implements(tInfo.nonPtrType) {
						v.Set(reflect.New(registeredType))
						v = v.Elem()
						tInfo = getTypeInfo(registeredType)
					}
				}
			}
		}
	}

	// Create new value for the pointer v to point to.
	// At this point, CBOR value is not nil/undefined if v is a pointer.
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			if !v.CanSet() {
				d.skip()
				return errors.New("cbor: cannot set new value for " + v.Type().String())
			}
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}

	// Strip self-described CBOR tag number.
	for d.nextCBORType() == cborTypeTag {
		off := d.off
		_, _, tagNum := d.getHead()
		if tagNum != selfDescribedCBORTagNum {
			d.off = off
			break
		}
	}

	// Check validity of supported built-in tags.
	off := d.off
	for d.nextCBORType() == cborTypeTag {
		_, _, tagNum := d.getHead()
		if err := validBuiltinTag(tagNum, d.data[d.off]); err != nil {
			d.skip()
			return err
		}
	}
	d.off = off

	if tInfo.spclType != specialTypeNone {
		switch tInfo.spclType {
		case specialTypeEmptyIface:
			iv, err := d.parse(false) // Skipped self-described CBOR tag number already.
			if iv != nil {
				v.Set(reflect.ValueOf(iv))
			}
			return err
		case specialTypeTag:
			return d.parseToTag(v)
		case specialTypeTime:
			if d.nextCBORNil() {
				// Decoding CBOR null and undefined to time.Time is no-op.
				d.skip()
				return nil
			}
			tm, ok, err := d.parseToTime()
			if err != nil {
				return err
			}
			if ok {
				v.Set(reflect.ValueOf(tm))
			}
			return nil
		case specialTypeUnmarshalerIface:
			return d.parseToUnmarshaler(v)
		}
	}

	// Check registered tag number
	if tagItem := d.getRegisteredTagItem(tInfo.nonPtrType); tagItem != nil {
		t := d.nextCBORType()
		if t != cborTypeTag {
			if tagItem.opts.DecTag == DecTagRequired {
				d.skip() // Required tag number is absent, skip entire tag
				return &UnmarshalTypeError{
					CBORType: t.String(),
					GoType:   tInfo.typ.String(),
					errorMsg: "expect CBOR tag value"}
			}
		} else if err := d.validRegisteredTagNums(tagItem); err != nil {
			d.skip() // Skip tag content
			return err
		}
	}

	t := d.nextCBORType()

	switch t {
	case cborTypePositiveInt:
		_, _, val := d.getHead()
		return fillPositiveInt(t, val, v)

	case cborTypeNegativeInt:
		_, _, val := d.getHead()
		if val > math.MaxInt64 {
			// CBOR negative integer overflows int64, use big.Int to store value.
			bi := new(big.Int)
			bi.SetUint64(val)
			bi.Add(bi, big.NewInt(1))
			bi.Neg(bi)

			if tInfo.nonPtrType == typeBigInt {
				v.Set(reflect.ValueOf(*bi))
				return nil
			}
			return &UnmarshalTypeError{
				CBORType: t.String(),
				GoType:   tInfo.nonPtrType.String(),
				errorMsg: bi.String() + " overflows Go's int64",
			}
		}
		nValue := int64(-1) ^ int64(val)
		return fillNegativeInt(t, nValue, v)

	case cborTypeByteString:
		b, copied := d.parseByteString()
		return fillByteString(t, b, !copied, v, d.dm.byteStringToString)

	case cborTypeTextString:
		b, err := d.parseTextString()
		if err != nil {
			return err
		}
		return fillTextString(t, b, v)

	case cborTypePrimitives:
		_, ai, val := d.getHead()
		switch ai {
		case 25:
			f := float64(float16.Frombits(uint16(val)).Float32())
			return fillFloat(t, f, v)
		case 26:
			f := float64(math.Float32frombits(uint32(val)))
			return fillFloat(t, f, v)
		case 27:
			f := math.Float64frombits(val)
			return fillFloat(t, f, v)
		default: // ai <= 24
			if d.dm.simpleValues.rejected[SimpleValue(val)] {
				return &UnacceptableDataItemError{
					CBORType: t.String(),
					Message:  "simple value " + strconv.FormatInt(int64(val), 10) + " is not recognized",
				}
			}

			switch ai {
			case 20, 21:
				return fillBool(t, ai == 21, v)
			case 22, 23:
				return fillNil(t, v)
			default:
				return fillPositiveInt(t, val, v)
			}
		}

	case cborTypeTag:
		_, _, tagNum := d.getHead()
		switch tagNum {
		case 2:
			// Bignum (tag 2) can be decoded to uint, int, float, slice, array, or big.Int.
			b, copied := d.parseByteString()
			bi := new(big.Int).SetBytes(b)

			if tInfo.nonPtrType == typeBigInt {
				v.Set(reflect.ValueOf(*bi))
				return nil
			}
			if tInfo.nonPtrKind == reflect.Slice || tInfo.nonPtrKind == reflect.Array {
				return fillByteString(t, b, !copied, v, ByteStringToStringForbidden)
			}
			if bi.IsUint64() {
				return fillPositiveInt(t, bi.Uint64(), v)
			}
			return &UnmarshalTypeError{
				CBORType: t.String(),
				GoType:   tInfo.nonPtrType.String(),
				errorMsg: bi.String() + " overflows " + v.Type().String(),
			}
		case 3:
			// Bignum (tag 3) can be decoded to int, float, slice, array, or big.Int.
			b, copied := d.parseByteString()
			bi := new(big.Int).SetBytes(b)
			bi.Add(bi, big.NewInt(1))
			bi.Neg(bi)

			if tInfo.nonPtrType == typeBigInt {
				v.Set(reflect.ValueOf(*bi))
				return nil
			}
			if tInfo.nonPtrKind == reflect.Slice || tInfo.nonPtrKind == reflect.Array {
				return fillByteString(t, b, !copied, v, ByteStringToStringForbidden)
			}
			if bi.IsInt64() {
				return fillNegativeInt(t, bi.Int64(), v)
			}
			return &UnmarshalTypeError{
				CBORType: t.String(),
				GoType:   tInfo.nonPtrType.String(),
				errorMsg: bi.String() + " overflows " + v.Type().String(),
			}
		}
		return d.parseToValue(v, tInfo)

	case cborTypeArray:
		if tInfo.nonPtrKind == reflect.Slice {
			return d.parseArrayToSlice(v, tInfo)
		} else if tInfo.nonPtrKind == reflect.Array {
			return d.parseArrayToArray(v, tInfo)
		} else if tInfo.nonPtrKind == reflect.Struct {
			return d.parseArrayToStruct(v, tInfo)
		}
		d.skip()
		return &UnmarshalTypeError{CBORType: t.String(), GoType: tInfo.nonPtrType.String()}

	case cborTypeMap:
		if tInfo.nonPtrKind == reflect.Struct {
			return d.parseMapToStruct(v, tInfo)
		} else if tInfo.nonPtrKind == reflect.Map {
			return d.parseMapToMap(v, tInfo)
		}
		d.skip()
		return &UnmarshalTypeError{CBORType: t.String(), GoType: tInfo.nonPtrType.String()}
	}

	return nil
}

func (d *decoder) parseToTag(v reflect.Value) error {
	if d.nextCBORNil() {
		// Decoding CBOR null and undefined to cbor.Tag is no-op.
		d.skip()
		return nil
	}

	t := d.nextCBORType()
	if t != cborTypeTag {
		d.skip()
		return &UnmarshalTypeError{CBORType: t.String(), GoType: typeTag.String()}
	}

	// Unmarshal tag number
	_, _, num := d.getHead()

	// Unmarshal tag content
	content, err := d.parse(false)
	if err != nil {
		return err
	}

	v.Set(reflect.ValueOf(Tag{num, content}))
	return nil
}

// parseToTime decodes the current data item as a time.Time. The bool return value is false if and
// only if the destination value should remain unmodified.
func (d *decoder) parseToTime() (time.Time, bool, error) {
	// Verify that tag number or absence of tag number is acceptable to specified timeTag.
	if t := d.nextCBORType(); t == cborTypeTag {
		if d.dm.timeTag == DecTagIgnored {
			// Skip all enclosing tags
			for t == cborTypeTag {
				d.getHead()
				t = d.nextCBORType()
			}
			if d.nextCBORNil() {
				d.skip()
				return time.Time{}, false, nil
			}
		} else {
			// Read tag number
			_, _, tagNum := d.getHead()
			if tagNum != 0 && tagNum != 1 {
				d.skip() // skip tag content
				return time.Time{}, false, errors.New("cbor: wrong tag number for time.Time, got " + strconv.Itoa(int(tagNum)) + ", expect 0 or 1")
			}
		}
	} else {
		if d.dm.timeTag == DecTagRequired {
			d.skip()
			return time.Time{}, false, &UnmarshalTypeError{CBORType: t.String(), GoType: typeTime.String(), errorMsg: "expect CBOR tag value"}
		}
	}

	switch t := d.nextCBORType(); t {
	case cborTypeTextString:
		s, err := d.parseTextString()
		if err != nil {
			return time.Time{}, false, err
		}
		t, err := time.Parse(time.RFC3339, string(s))
		if err != nil {
			return time.Time{}, false, errors.New("cbor: cannot set " + string(s) + " for time.Time: " + err.Error())
		}
		return t, true, nil
	case cborTypePositiveInt:
		_, _, val := d.getHead()
		if val > math.MaxInt64 {
			return time.Time{}, false, &UnmarshalTypeError{
				CBORType: t.String(),
				GoType:   typeTime.String(),
				errorMsg: fmt.Sprintf("%d overflows Go's int64", val),
			}
		}
		return time.Unix(int64(val), 0), true, nil
	case cborTypeNegativeInt:
		_, _, val := d.getHead()
		if val > math.MaxInt64 {
			if val == math.MaxUint64 {
				// Maximum absolute value representable by negative integer is 2^64,
				// not 2^64-1, so it overflows uint64.
				return time.Time{}, false, &UnmarshalTypeError{
					CBORType: t.String(),
					GoType:   typeTime.String(),
					errorMsg: "-18446744073709551616 overflows Go's int64",
				}
			}
			return time.Time{}, false, &UnmarshalTypeError{
				CBORType: t.String(),
				GoType:   typeTime.String(),
				errorMsg: fmt.Sprintf("-%d overflows Go's int64", val+1),
			}
		}
		return time.Unix(int64(-1)^int64(val), 0), true, nil
	case cborTypePrimitives:
		_, ai, val := d.getHead()
		var f float64
		switch ai {
		case 25:
			f = float64(float16.Frombits(uint16(val)).Float32())
		case 26:
			f = float64(math.Float32frombits(uint32(val)))
		case 27:
			f = math.Float64frombits(val)
		default:
			return time.Time{}, false, &UnmarshalTypeError{CBORType: t.String(), GoType: typeTime.String()}
		}

		if math.IsNaN(f) || math.IsInf(f, 0) {
			// https://www.rfc-editor.org/rfc/rfc8949.html#section-3.4.2-6
			return time.Time{}, true, nil
		}
		seconds, fractional := math.Modf(f)
		return time.Unix(int64(seconds), int64(fractional*1e9)), true, nil
	default:
		return time.Time{}, false, &UnmarshalTypeError{CBORType: t.String(), GoType: typeTime.String()}
	}
}

// parseToUnmarshaler parses CBOR data to value implementing Unmarshaler interface.
// It assumes data is well-formed, and does not perform bounds checking.
func (d *decoder) parseToUnmarshaler(v reflect.Value) error {
	if d.nextCBORNil() && v.Kind() == reflect.Ptr && v.IsNil() {
		d.skip()
		return nil
	}

	if v.Kind() != reflect.Ptr && v.CanAddr() {
		v = v.Addr()
	}
	if u, ok := v.Interface().(Unmarshaler); ok {
		start := d.off
		d.skip()
		return u.UnmarshalCBOR(d.data[start:d.off])
	}
	d.skip()
	return errors.New("cbor: failed to assert " + v.Type().String() + " as cbor.Unmarshaler")
}

// parse parses CBOR data and returns value in default Go type.
// It assumes data is well-formed, and does not perform bounds checking.
func (d *decoder) parse(skipSelfDescribedTag bool) (interface{}, error) { //nolint:gocyclo
	// Strip self-described CBOR tag number.
	if skipSelfDescribedTag {
		for d.nextCBORType() == cborTypeTag {
			off := d.off
			_, _, tagNum := d.getHead()
			if tagNum != selfDescribedCBORTagNum {
				d.off = off
				break
			}
		}
	}

	// Check validity of supported built-in tags.
	off := d.off
	for d.nextCBORType() == cborTypeTag {
		_, _, tagNum := d.getHead()
		if err := validBuiltinTag(tagNum, d.data[d.off]); err != nil {
			d.skip()
			return nil, err
		}
	}
	d.off = off

	t := d.nextCBORType()
	switch t {
	case cborTypePositiveInt:
		_, _, val := d.getHead()

		switch d.dm.intDec {
		case IntDecConvertNone:
			return val, nil

		case IntDecConvertSigned, IntDecConvertSignedOrFail:
			if val > math.MaxInt64 {
				return nil, &UnmarshalTypeError{
					CBORType: t.String(),
					GoType:   reflect.TypeOf(int64(0)).String(),
					errorMsg: strconv.FormatUint(val, 10) + " overflows Go's int64",
				}
			}

			return int64(val), nil

		case IntDecConvertSignedOrBigInt:
			if val > math.MaxInt64 {
				bi := new(big.Int).SetUint64(val)
				if d.dm.bigIntDec == BigIntDecodePointer {
					return bi, nil
				}
				return *bi, nil
			}

			return int64(val), nil

		default:
			// not reachable
		}

	case cborTypeNegativeInt:
		_, _, val := d.getHead()

		if val > math.MaxInt64 {
			// CBOR negative integer value overflows Go int64, use big.Int instead.
			bi := new(big.Int).SetUint64(val)
			bi.Add(bi, big.NewInt(1))
			bi.Neg(bi)

			if d.dm.intDec == IntDecConvertSignedOrFail {
				return nil, &UnmarshalTypeError{
					CBORType: t.String(),
					GoType:   reflect.TypeOf(int64(0)).String(),
					errorMsg: bi.String() + " overflows Go's int64",
				}
			}

			if d.dm.bigIntDec == BigIntDecodePointer {
				return bi, nil
			}
			return *bi, nil
		}

		nValue := int64(-1) ^ int64(val)
		return nValue, nil

	case cborTypeByteString:
		switch d.dm.defaultByteStringType {
		case nil, typeByteSlice:
			b, copied := d.parseByteString()
			if copied {
				return b, nil
			}
			clone := make([]byte, len(b))
			copy(clone, b)
			return clone, nil
		case typeString:
			b, _ := d.parseByteString()
			return string(b), nil
		default:
			b, copied := d.parseByteString()
			if copied || d.dm.defaultByteStringType.Kind() == reflect.String {
				// Avoid an unnecessary copy since the conversion to string must
				// copy the underlying bytes.
				return reflect.ValueOf(b).Convert(d.dm.defaultByteStringType).Interface(), nil
			}
			clone := make([]byte, len(b))
			copy(clone, b)
			return reflect.ValueOf(clone).Convert(d.dm.defaultByteStringType).Interface(), nil
		}
	case cborTypeTextString:
		b, err := d.parseTextString()
		if err != nil {
			return nil, err
		}
		return string(b), nil
	case cborTypeTag:
		tagOff := d.off
		_, _, tagNum := d.getHead()
		contentOff := d.off

		switch tagNum {
		case 0, 1:
			d.off = tagOff
			tm, _, err := d.parseToTime()
			if err != nil {
				return nil, err
			}
			switch d.dm.timeTagToAny {
			case TimeTagToTime:
				return tm, nil
			case TimeTagToRFC3339:
				if tagNum == 1 {
					tm = tm.UTC()
				}
				return tm.Format(time.RFC3339), nil
			case TimeTagToRFC3339Nano:
				if tagNum == 1 {
					tm = tm.UTC()
				}
				return tm.Format(time.RFC3339Nano), nil
			default:
				// not reachable
			}
		case 2:
			b, _ := d.parseByteString()
			bi := new(big.Int).SetBytes(b)

			if d.dm.bigIntDec == BigIntDecodePointer {
				return bi, nil
			}
			return *bi, nil
		case 3:
			b, _ := d.parseByteString()
			bi := new(big.Int).SetBytes(b)
			bi.Add(bi, big.NewInt(1))
			bi.Neg(bi)

			if d.dm.bigIntDec == BigIntDecodePointer {
				return bi, nil
			}
			return *bi, nil
		}

		if d.dm.tags != nil {
			// Parse to specified type if tag number is registered.
			tagNums := []uint64{tagNum}
			for d.nextCBORType() == cborTypeTag {
				_, _, num := d.getHead()
				tagNums = append(tagNums, num)
			}
			registeredType := d.dm.tags.getTypeFromTagNum(tagNums)
			if registeredType != nil {
				d.off = tagOff
				rv := reflect.New(registeredType)
				if err := d.parseToValue(rv.Elem(), getTypeInfo(registeredType)); err != nil {
					return nil, err
				}
				return rv.Elem().Interface(), nil
			}
		}

		// Parse tag content
		d.off = contentOff
		content, err := d.parse(false)
		if err != nil {
			return nil, err
		}
		if d.dm.unrecognizedTagToAny == UnrecognizedTagContentToAny {
			return content, nil
		}
		return Tag{tagNum, content}, nil
	case cborTypePrimitives:
		_, ai, val := d.getHead()
		if ai <= 24 && d.dm.simpleValues.rejected[SimpleValue(val)] {
			return nil, &UnacceptableDataItemError{
				CBORType: t.String(),
				Message:  "simple value " + strconv.FormatInt(int64(val), 10) + " is not recognized",
			}
		}
		if ai < 20 || ai == 24 {
			return SimpleValue(val), nil
		}
		switch ai {
		case 20, 21:
			return (ai == 21), nil
		case 22, 23:
			return nil, nil
		case 25:
			f := float64(float16.Frombits(uint16(val)).Float32())
			return f, nil
		case 26:
			f := float64(math.Float32frombits(uint32(val)))
			return f, nil
		case 27:
			f := math.Float64frombits(val)
			return f, nil
		}
	case cborTypeArray:
		return d.parseArray()
	case cborTypeMap:
		if d.dm.defaultMapType != nil {
			m := reflect.New(d.dm.defaultMapType)
			err := d.parseToValue(m, getTypeInfo(m.Elem().Type()))
			if err != nil {
				return nil, err
			}
			return m.Elem().Interface(), nil
		}
		return d.parseMap()
	}
	return nil, nil
}

// parseByteString parses a CBOR encoded byte string. The returned byte slice
// may be backed directly by the input. The second return value will be true if
// and only if the slice is backed by a copy of the input. Callers are
// responsible for making a copy if necessary.
func (d *decoder) parseByteString() ([]byte, bool) {
	_, ai, val := d.getHead()
	if ai != 31 {
		b := d.data[d.off : d.off+int(val)]
		d.off += int(val)
		return b, false
	}
	// Process indefinite length string chunks.
	b := []byte{}
	for !d.foundBreak() {
		_, _, val = d.getHead()
		b = append(b, d.data[d.off:d.off+int(val)]...)
		d.off += int(val)
	}
	return b, true
}

// parseTextString parses CBOR encoded text string.  It returns a byte slice
// to prevent creating an extra copy of string.  Caller should wrap returned
// byte slice as string when needed.
func (d *decoder) parseTextString() ([]byte, error) {
	_, ai, val := d.getHead()
	if ai != 31 {
		b := d.data[d.off : d.off+int(val)]
		d.off += int(val)
		if d.dm.utf8 == UTF8RejectInvalid && !utf8.Valid(b) {
			return nil, &SemanticError{"cbor: invalid UTF-8 string"}
		}
		return b, nil
	}
	// Process indefinite length string chunks.
	b := []byte{}
	for !d.foundBreak() {
		_, _, val = d.getHead()
		x := d.data[d.off : d.off+int(val)]
		d.off += int(val)
		if d.dm.utf8 == UTF8RejectInvalid && !utf8.Valid(x) {
			for !d.foundBreak() {
				d.skip() // Skip remaining chunk on error
			}
			return nil, &SemanticError{"cbor: invalid UTF-8 string"}
		}
		b = append(b, x...)
	}
	return b, nil
}

func (d *decoder) parseArray() ([]interface{}, error) {
	_, ai, val := d.getHead()
	hasSize := (ai != 31)
	count := int(val)
	if !hasSize {
		count = d.numOfItemsUntilBreak() // peek ahead to get array size to preallocate slice for better performance
	}
	v := make([]interface{}, count)
	var e interface{}
	var err, lastErr error
	for i := 0; (hasSize && i < count) || (!hasSize && !d.foundBreak()); i++ {
		if e, lastErr = d.parse(true); lastErr != nil {
			if err == nil {
				err = lastErr
			}
			continue
		}
		v[i] = e
	}
	return v, err
}

func (d *decoder) parseArrayToSlice(v reflect.Value, tInfo *typeInfo) error {
	_, ai, val := d.getHead()
	hasSize := (ai != 31)
	count := int(val)
	if !hasSize {
		count = d.numOfItemsUntilBreak() // peek ahead to get array size to preallocate slice for better performance
	}
	if v.IsNil() || v.Cap() < count || count == 0 {
		v.Set(reflect.MakeSlice(tInfo.nonPtrType, count, count))
	}
	v.SetLen(count)
	var err error
	for i := 0; (hasSize && i < count) || (!hasSize && !d.foundBreak()); i++ {
		if lastErr := d.parseToValue(v.Index(i), tInfo.elemTypeInfo); lastErr != nil {
			if err == nil {
				err = lastErr
			}
		}
	}
	return err
}

func (d *decoder) parseArrayToArray(v reflect.Value, tInfo *typeInfo) error {
	_, ai, val := d.getHead()
	hasSize := (ai != 31)
	count := int(val)
	gi := 0
	vLen := v.Len()
	var err error
	for ci := 0; (hasSize && ci < count) || (!hasSize && !d.foundBreak()); ci++ {
		if gi < vLen {
			// Read CBOR array element and set array element
			if lastErr := d.parseToValue(v.Index(gi), tInfo.elemTypeInfo); lastErr != nil {
				if err == nil {
					err = lastErr
				}
			}
			gi++
		} else {
			d.skip() // Skip remaining CBOR array element
		}
	}
	// Set remaining Go array elements to zero values.
	if gi < vLen {
		zeroV := reflect.Zero(tInfo.elemTypeInfo.typ)
		for ; gi < vLen; gi++ {
			v.Index(gi).Set(zeroV)
		}
	}
	return err
}

func (d *decoder) parseMap() (interface{}, error) {
	_, ai, val := d.getHead()
	hasSize := (ai != 31)
	count := int(val)
	m := make(map[interface{}]interface{})
	var k, e interface{}
	var err, lastErr error
	keyCount := 0
	for i := 0; (hasSize && i < count) || (!hasSize && !d.foundBreak()); i++ {
		// Parse CBOR map key.
		if k, lastErr = d.parse(true); lastErr != nil {
			if err == nil {
				err = lastErr
			}
			d.skip()
			continue
		}

		// Detect if CBOR map key can be used as Go map key.
		rv := reflect.ValueOf(k)
		if !isHashableValue(rv) {
			var converted bool
			if d.dm.mapKeyByteString == MapKeyByteStringAllowed {
				k, converted = convertByteSliceToByteString(k)
			}
			if !converted {
				if err == nil {
					err = &InvalidMapKeyTypeError{rv.Type().String()}
				}
				d.skip()
				continue
			}
		}

		// Parse CBOR map value.
		if e, lastErr = d.parse(true); lastErr != nil {
			if err == nil {
				err = lastErr
			}
			continue
		}

		// Add key-value pair to Go map.
		m[k] = e

		// Detect duplicate map key.
		if d.dm.dupMapKey == DupMapKeyEnforcedAPF {
			newKeyCount := len(m)
			if newKeyCount == keyCount {
				m[k] = nil
				err = &DupMapKeyError{k, i}
				i++
				// skip the rest of the map
				for ; (hasSize && i < count) || (!hasSize && !d.foundBreak()); i++ {
					d.skip() // Skip map key
					d.skip() // Skip map value
				}
				return m, err
			}
			keyCount = newKeyCount
		}
	}
	return m, err
}

func (d *decoder) parseMapToMap(v reflect.Value, tInfo *typeInfo) error { //nolint:gocyclo
	_, ai, val := d.getHead()
	hasSize := (ai != 31)
	count := int(val)
	if v.IsNil() {
		mapsize := count
		if !hasSize {
			mapsize = 0
		}
		v.Set(reflect.MakeMapWithSize(tInfo.nonPtrType, mapsize))
	}
	keyType, eleType := tInfo.keyTypeInfo.typ, tInfo.elemTypeInfo.typ
	reuseKey, reuseEle := isImmutableKind(tInfo.keyTypeInfo.kind), isImmutableKind(tInfo.elemTypeInfo.kind)
	var keyValue, eleValue, zeroKeyValue, zeroEleValue reflect.Value
	keyIsInterfaceType := keyType == typeIntf // If key type is interface{}, need to check if key value is hashable.
	var err, lastErr error
	keyCount := v.Len()
	var existingKeys map[interface{}]bool // Store existing map keys, used for detecting duplicate map key.
	if d.dm.dupMapKey == DupMapKeyEnforcedAPF {
		existingKeys = make(map[interface{}]bool, keyCount)
		if keyCount > 0 {
			vKeys := v.MapKeys()
			for i := 0; i < len(vKeys); i++ {
				existingKeys[vKeys[i].Interface()] = true
			}
		}
	}
	for i := 0; (hasSize && i < count) || (!hasSize && !d.foundBreak()); i++ {
		// Parse CBOR map key.
		if !keyValue.IsValid() {
			keyValue = reflect.New(keyType).Elem()
		} else if !reuseKey {
			if !zeroKeyValue.IsValid() {
				zeroKeyValue = reflect.Zero(keyType)
			}
			keyValue.Set(zeroKeyValue)
		}
		if lastErr = d.parseToValue(keyValue, tInfo.keyTypeInfo); lastErr != nil {
			if err == nil {
				err = lastErr
			}
			d.skip()
			continue
		}

		// Detect if CBOR map key can be used as Go map key.
		if keyIsInterfaceType && keyValue.Elem().IsValid() {
			if !isHashableValue(keyValue.Elem()) {
				var converted bool
				if d.dm.mapKeyByteString == MapKeyByteStringAllowed {
					var k interface{}
					k, converted = convertByteSliceToByteString(keyValue.Elem().Interface())
					if converted {
						keyValue.Set(reflect.ValueOf(k))
					}
				}
				if !converted {
					if err == nil {
						err = &InvalidMapKeyTypeError{keyValue.Elem().Type().String()}
					}
					d.skip()
					continue
				}
			}
		}

		// Parse CBOR map value.
		if !eleValue.IsValid() {
			eleValue = reflect.New(eleType).Elem()
		} else if !reuseEle {
			if !zeroEleValue.IsValid() {
				zeroEleValue = reflect.Zero(eleType)
			}
			eleValue.Set(zeroEleValue)
		}
		if lastErr := d.parseToValue(eleValue, tInfo.elemTypeInfo); lastErr != nil {
			if err == nil {
				err = lastErr
			}
			continue
		}

		// Add key-value pair to Go map.
		v.SetMapIndex(keyValue, eleValue)

		// Detect duplicate map key.
		if d.dm.dupMapKey == DupMapKeyEnforcedAPF {
			newKeyCount := v.Len()
			if newKeyCount == keyCount {
				kvi := keyValue.Interface()
				if !existingKeys[kvi] {
					v.SetMapIndex(keyValue, reflect.New(eleType).Elem())
					err = &DupMapKeyError{kvi, i}
					i++
					// skip the rest of the map
					for ; (hasSize && i < count) || (!hasSize && !d.foundBreak()); i++ {
						d.skip() // skip map key
						d.skip() // skip map value
					}
					return err
				}
				delete(existingKeys, kvi)
			}
			keyCount = newKeyCount
		}
	}
	return err
}

func (d *decoder) parseArrayToStruct(v reflect.Value, tInfo *typeInfo) error {
	structType := getDecodingStructType(tInfo.nonPtrType)
	if structType.err != nil {
		return structType.err
	}

	if !structType.toArray {
		t := d.nextCBORType()
		d.skip()
		return &UnmarshalTypeError{
			CBORType: t.String(),
			GoType:   tInfo.nonPtrType.String(),
			errorMsg: "cannot decode CBOR array to struct without toarray option",
		}
	}

	start := d.off
	t, ai, val := d.getHead()
	hasSize := (ai != 31)
	count := int(val)
	if !hasSize {
		count = d.numOfItemsUntilBreak() // peek ahead to get array size
	}
	if count != len(structType.fields) {
		d.off = start
		d.skip()
		return &UnmarshalTypeError{
			CBORType: t.String(),
			GoType:   tInfo.typ.String(),
			errorMsg: "cannot decode CBOR array to struct with different number of elements",
		}
	}
	var err, lastErr error
	for i := 0; (hasSize && i < count) || (!hasSize && !d.foundBreak()); i++ {
		f := structType.fields[i]

		// Get field value by index
		var fv reflect.Value
		if len(f.idx) == 1 {
			fv = v.Field(f.idx[0])
		} else {
			fv, lastErr = getFieldValue(v, f.idx, func(v reflect.Value) (reflect.Value, error) {
				// Return a new value for embedded field null pointer to point to, or return error.
				if !v.CanSet() {
					return reflect.Value{}, errors.New("cbor: cannot set embedded pointer to unexported struct: " + v.Type().String())
				}
				v.Set(reflect.New(v.Type().Elem()))
				return v, nil
			})
			if lastErr != nil && err == nil {
				err = lastErr
			}
			if !fv.IsValid() {
				d.skip()
				continue
			}
		}

		if lastErr = d.parseToValue(fv, f.typInfo); lastErr != nil {
			if err == nil {
				if typeError, ok := lastErr.(*UnmarshalTypeError); ok {
					typeError.StructFieldName = tInfo.typ.String() + "." + f.name
					err = typeError
				} else {
					err = lastErr
				}
			}
		}
	}
	return err
}

// parseMapToStruct needs to be fast so gocyclo can be ignored for now.
func (d *decoder) parseMapToStruct(v reflect.Value, tInfo *typeInfo) error { //nolint:gocyclo
	structType := getDecodingStructType(tInfo.nonPtrType)
	if structType.err != nil {
		return structType.err
	}

	if structType.toArray {
		t := d.nextCBORType()
		d.skip()
		return &UnmarshalTypeError{
			CBORType: t.String(),
			GoType:   tInfo.nonPtrType.String(),
			errorMsg: "cannot decode CBOR map to struct with toarray option",
		}
	}

	var err, lastErr error

	// Get CBOR map size
	_, ai, val := d.getHead()
	hasSize := (ai != 31)
	count := int(val)

	// Keeps track of matched struct fields
	var foundFldIdx []bool
	{
		const maxStackFields = 128
		if nfields := len(structType.fields); nfields <= maxStackFields {
			// For structs with typical field counts, expect that this can be
			// stack-allocated.
			var a [maxStackFields]bool
			foundFldIdx = a[:nfields]
		} else {
			foundFldIdx = make([]bool, len(structType.fields))
		}
	}

	// Keeps track of CBOR map keys to detect duplicate map key
	keyCount := 0
	var mapKeys map[interface{}]struct{}

	errOnUnknownField := (d.dm.extraReturnErrors & ExtraDecErrorUnknownField) > 0

MapEntryLoop:
	for j := 0; (hasSize && j < count) || (!hasSize && !d.foundBreak()); j++ {
		var f *field

		// If duplicate field detection is enabled and the key at index j did not match any
		// field, k will hold the map key.
		var k interface{}

		t := d.nextCBORType()
		if t == cborTypeTextString || (t == cborTypeByteString && d.dm.fieldNameByteString == FieldNameByteStringAllowed) {
			var keyBytes []byte
			if t == cborTypeTextString {
				keyBytes, lastErr = d.parseTextString()
				if lastErr != nil {
					if err == nil {
						err = lastErr
					}
					d.skip() // skip value
					continue
				}
			} else { // cborTypeByteString
				keyBytes, _ = d.parseByteString()
			}

			// Check for exact match on field name.
			if i, ok := structType.fieldIndicesByName[string(keyBytes)]; ok {
				fld := structType.fields[i]

				if !foundFldIdx[i] {
					f = fld
					foundFldIdx[i] = true
				} else if d.dm.dupMapKey == DupMapKeyEnforcedAPF {
					err = &DupMapKeyError{fld.name, j}
					d.skip() // skip value
					j++
					// skip the rest of the map
					for ; (hasSize && j < count) || (!hasSize && !d.foundBreak()); j++ {
						d.skip()
						d.skip()
					}
					return err
				} else {
					// discard repeated match
					d.skip()
					continue MapEntryLoop
				}
			}

			// Find field with case-insensitive match
			if f == nil && d.dm.fieldNameMatching == FieldNameMatchingPreferCaseSensitive {
				keyLen := len(keyBytes)
				keyString := string(keyBytes)
				for i := 0; i < len(structType.fields); i++ {
					fld := structType.fields[i]
					if len(fld.name) == keyLen && strings.EqualFold(fld.name, keyString) {
						if !foundFldIdx[i] {
							f = fld
							foundFldIdx[i] = true
						} else if d.dm.dupMapKey == DupMapKeyEnforcedAPF {
							err = &DupMapKeyError{keyString, j}
							d.skip() // skip value
							j++
							// skip the rest of the map
							for ; (hasSize && j < count) || (!hasSize && !d.foundBreak()); j++ {
								d.skip()
								d.skip()
							}
							return err
						} else {
							// discard repeated match
							d.skip()
							continue MapEntryLoop
						}
						break
					}
				}
			}

			if d.dm.dupMapKey == DupMapKeyEnforcedAPF && f == nil {
				k = string(keyBytes)
			}
		} else if t <= cborTypeNegativeInt { // uint/int
			var nameAsInt int64

			if t == cborTypePositiveInt {
				_, _, val := d.getHead()
				nameAsInt = int64(val)
			} else {
				_, _, val := d.getHead()
				if val > math.MaxInt64 {
					if err == nil {
						err = &UnmarshalTypeError{
							CBORType: t.String(),
							GoType:   reflect.TypeOf(int64(0)).String(),
							errorMsg: "-1-" + strconv.FormatUint(val, 10) + " overflows Go's int64",
						}
					}
					d.skip() // skip value
					continue
				}
				nameAsInt = int64(-1) ^ int64(val)
			}

			// Find field
			for i := 0; i < len(structType.fields); i++ {
				fld := structType.fields[i]
				if fld.keyAsInt && fld.nameAsInt == nameAsInt {
					if !foundFldIdx[i] {
						f = fld
						foundFldIdx[i] = true
					} else if d.dm.dupMapKey == DupMapKeyEnforcedAPF {
						err = &DupMapKeyError{nameAsInt, j}
						d.skip() // skip value
						j++
						// skip the rest of the map
						for ; (hasSize && j < count) || (!hasSize && !d.foundBreak()); j++ {
							d.skip()
							d.skip()
						}
						return err
					} else {
						// discard repeated match
						d.skip()
						continue MapEntryLoop
					}
					break
				}
			}

			if d.dm.dupMapKey == DupMapKeyEnforcedAPF && f == nil {
				k = nameAsInt
			}
		} else {
			if err == nil {
				err = &UnmarshalTypeError{
					CBORType: t.String(),
					GoType:   reflect.TypeOf("").String(),
					errorMsg: "map key is of type " + t.String() + " and cannot be used to match struct field name",
				}
			}
			if d.dm.dupMapKey == DupMapKeyEnforcedAPF {
				// parse key
				k, lastErr = d.parse(true)
				if lastErr != nil {
					d.skip() // skip value
					continue
				}
				// Detect if CBOR map key can be used as Go map key.
				if !isHashableValue(reflect.ValueOf(k)) {
					d.skip() // skip value
					continue
				}
			} else {
				d.skip() // skip key
			}
		}

		if f == nil {
			if errOnUnknownField {
				err = &UnknownFieldError{j}
				d.skip() // Skip value
				j++
				// skip the rest of the map
				for ; (hasSize && j < count) || (!hasSize && !d.foundBreak()); j++ {
					d.skip()
					d.skip()
				}
				return err
			}

			// Two map keys that match the same struct field are immediately considered
			// duplicates. This check detects duplicates between two map keys that do
			// not match a struct field. If unknown field errors are enabled, then this
			// check is never reached.
			if d.dm.dupMapKey == DupMapKeyEnforcedAPF {
				if mapKeys == nil {
					mapKeys = make(map[interface{}]struct{}, 1)
				}
				mapKeys[k] = struct{}{}
				newKeyCount := len(mapKeys)
				if newKeyCount == keyCount {
					err = &DupMapKeyError{k, j}
					d.skip() // skip value
					j++
					// skip the rest of the map
					for ; (hasSize && j < count) || (!hasSize && !d.foundBreak()); j++ {
						d.skip()
						d.skip()
					}
					return err
				}
				keyCount = newKeyCount
			}

			d.skip() // Skip value
			continue
		}

		// Get field value by index
		var fv reflect.Value
		if len(f.idx) == 1 {
			fv = v.Field(f.idx[0])
		} else {
			fv, lastErr = getFieldValue(v, f.idx, func(v reflect.Value) (reflect.Value, error) {
				// Return a new value for embedded field null pointer to point to, or return error.
				if !v.CanSet() {
					return reflect.Value{}, errors.New("cbor: cannot set embedded pointer to unexported struct: " + v.Type().String())
				}
				v.Set(reflect.New(v.Type().Elem()))
				return v, nil
			})
			if lastErr != nil && err == nil {
				err = lastErr
			}
			if !fv.IsValid() {
				d.skip()
				continue
			}
		}

		if lastErr = d.parseToValue(fv, f.typInfo); lastErr != nil {
			if err == nil {
				if typeError, ok := lastErr.(*UnmarshalTypeError); ok {
					typeError.StructFieldName = tInfo.nonPtrType.String() + "." + f.name
					err = typeError
				} else {
					err = lastErr
				}
			}
		}
	}
	return err
}

// validRegisteredTagNums verifies that tag numbers match registered tag numbers of type t.
// validRegisteredTagNums assumes next CBOR data type is tag.  It scans all tag numbers, and stops at tag content.
func (d *decoder) validRegisteredTagNums(registeredTag *tagItem) error {
	// Scan until next cbor data is tag content.
	tagNums := make([]uint64, 0, 1)
	for d.nextCBORType() == cborTypeTag {
		_, _, val := d.getHead()
		tagNums = append(tagNums, val)
	}

	if !registeredTag.equalTagNum(tagNums) {
		return &WrongTagError{registeredTag.contentType, registeredTag.num, tagNums}
	}
	return nil
}

func (d *decoder) getRegisteredTagItem(vt reflect.Type) *tagItem {
	if d.dm.tags != nil {
		return d.dm.tags.getTagItemFromType(vt)
	}
	return nil
}

// skip moves data offset to the next item.  skip assumes data is well-formed,
// and does not perform bounds checking.
func (d *decoder) skip() {
	t, ai, val := d.getHead()

	if ai == 31 {
		switch t {
		case cborTypeByteString, cborTypeTextString, cborTypeArray, cborTypeMap:
			for {
				if d.data[d.off] == 0xff {
					d.off++
					return
				}
				d.skip()
			}
		}
	}

	switch t {
	case cborTypeByteString, cborTypeTextString:
		d.off += int(val)
	case cborTypeArray:
		for i := 0; i < int(val); i++ {
			d.skip()
		}
	case cborTypeMap:
		for i := 0; i < int(val)*2; i++ {
			d.skip()
		}
	case cborTypeTag:
		d.skip()
	}
}

// getHead assumes data is well-formed, and does not perform bounds checking.
func (d *decoder) getHead() (t cborType, ai byte, val uint64) {
	t = cborType(d.data[d.off] & 0xe0)
	ai = d.data[d.off] & 0x1f
	val = uint64(ai)
	d.off++

	if ai < 24 {
		return
	}
	if ai == 24 {
		val = uint64(d.data[d.off])
		d.off++
		return
	}
	if ai == 25 {
		val = uint64(binary.BigEndian.Uint16(d.data[d.off : d.off+2]))
		d.off += 2
		return
	}
	if ai == 26 {
		val = uint64(binary.BigEndian.Uint32(d.data[d.off : d.off+4]))
		d.off += 4
		return
	}
	if ai == 27 {
		val = binary.BigEndian.Uint64(d.data[d.off : d.off+8])
		d.off += 8
		return
	}
	return
}

func (d *decoder) numOfItemsUntilBreak() int {
	savedOff := d.off
	i := 0
	for !d.foundBreak() {
		d.skip()
		i++
	}
	d.off = savedOff
	return i
}

// foundBreak assumes data is well-formed, and does not perform bounds checking.
func (d *decoder) foundBreak() bool {
	if d.data[d.off] == 0xff {
		d.off++
		return true
	}
	return false
}

func (d *decoder) reset(data []byte) {
	d.data = data
	d.off = 0
}

func (d *decoder) nextCBORType() cborType {
	return cborType(d.data[d.off] & 0xe0)
}

func (d *decoder) nextCBORNil() bool {
	return d.data[d.off] == 0xf6 || d.data[d.off] == 0xf7
}

var (
	typeIntf              = reflect.TypeOf([]interface{}(nil)).Elem()
	typeTime              = reflect.TypeOf(time.Time{})
	typeBigInt            = reflect.TypeOf(big.Int{})
	typeUnmarshaler       = reflect.TypeOf((*Unmarshaler)(nil)).Elem()
	typeBinaryUnmarshaler = reflect.TypeOf((*encoding.BinaryUnmarshaler)(nil)).Elem()
	typeString            = reflect.TypeOf("")
	typeByteSlice         = reflect.TypeOf([]byte(nil))
)

func fillNil(_ cborType, v reflect.Value) error {
	switch v.Kind() {
	case reflect.Slice, reflect.Map, reflect.Interface, reflect.Ptr:
		v.Set(reflect.Zero(v.Type()))
		return nil
	}
	return nil
}

func fillPositiveInt(t cborType, val uint64, v reflect.Value) error {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if val > math.MaxInt64 {
			return &UnmarshalTypeError{
				CBORType: t.String(),
				GoType:   v.Type().String(),
				errorMsg: strconv.FormatUint(val, 10) + " overflows " + v.Type().String(),
			}
		}
		if v.OverflowInt(int64(val)) {
			return &UnmarshalTypeError{
				CBORType: t.String(),
				GoType:   v.Type().String(),
				errorMsg: strconv.FormatUint(val, 10) + " overflows " + v.Type().String(),
			}
		}
		v.SetInt(int64(val))
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if v.OverflowUint(val) {
			return &UnmarshalTypeError{
				CBORType: t.String(),
				GoType:   v.Type().String(),
				errorMsg: strconv.FormatUint(val, 10) + " overflows " + v.Type().String(),
			}
		}
		v.SetUint(val)
		return nil
	case reflect.Float32, reflect.Float64:
		f := float64(val)
		v.SetFloat(f)
		return nil
	}
	if v.Type() == typeBigInt {
		i := new(big.Int).SetUint64(val)
		v.Set(reflect.ValueOf(*i))
		return nil
	}
	return &UnmarshalTypeError{CBORType: t.String(), GoType: v.Type().String()}
}

func fillNegativeInt(t cborType, val int64, v reflect.Value) error {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v.OverflowInt(val) {
			return &UnmarshalTypeError{
				CBORType: t.String(),
				GoType:   v.Type().String(),
				errorMsg: strconv.FormatInt(val, 10) + " overflows " + v.Type().String(),
			}
		}
		v.SetInt(val)
		return nil
	case reflect.Float32, reflect.Float64:
		f := float64(val)
		v.SetFloat(f)
		return nil
	}
	if v.Type() == typeBigInt {
		i := new(big.Int).SetInt64(val)
		v.Set(reflect.ValueOf(*i))
		return nil
	}
	return &UnmarshalTypeError{CBORType: t.String(), GoType: v.Type().String()}
}

func fillBool(t cborType, val bool, v reflect.Value) error {
	if v.Kind() == reflect.Bool {
		v.SetBool(val)
		return nil
	}
	return &UnmarshalTypeError{CBORType: t.String(), GoType: v.Type().String()}
}

func fillFloat(t cborType, val float64, v reflect.Value) error {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		if v.OverflowFloat(val) {
			return &UnmarshalTypeError{
				CBORType: t.String(),
				GoType:   v.Type().String(),
				errorMsg: strconv.FormatFloat(val, 'E', -1, 64) + " overflows " + v.Type().String(),
			}
		}
		v.SetFloat(val)
		return nil
	}
	return &UnmarshalTypeError{CBORType: t.String(), GoType: v.Type().String()}
}

func fillByteString(t cborType, val []byte, shared bool, v reflect.Value, bsts ByteStringToStringMode) error {
	if reflect.PtrTo(v.Type()).Implements(typeBinaryUnmarshaler) {
		if v.CanAddr() {
			v = v.Addr()
			if u, ok := v.Interface().(encoding.BinaryUnmarshaler); ok {
				// The contract of BinaryUnmarshaler forbids
				// retaining the input bytes, so no copying is
				// required even if val is shared.
				return u.UnmarshalBinary(val)
			}
		}
		return errors.New("cbor: cannot set new value for " + v.Type().String())
	}
	if bsts == ByteStringToStringAllowed && v.Kind() == reflect.String {
		v.SetString(string(val))
		return nil
	}
	if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
		src := val
		if shared {
			// SetBytes shares the underlying bytes of the source slice.
			src = make([]byte, len(val))
			copy(src, val)
		}
		v.SetBytes(src)
		return nil
	}
	if v.Kind() == reflect.Array && v.Type().Elem().Kind() == reflect.Uint8 {
		vLen := v.Len()
		i := 0
		for ; i < vLen && i < len(val); i++ {
			v.Index(i).SetUint(uint64(val[i]))
		}
		// Set remaining Go array elements to zero values.
		if i < vLen {
			zeroV := reflect.Zero(reflect.TypeOf(byte(0)))
			for ; i < vLen; i++ {
				v.Index(i).Set(zeroV)
			}
		}
		return nil
	}
	return &UnmarshalTypeError{CBORType: t.String(), GoType: v.Type().String()}
}

func fillTextString(t cborType, val []byte, v reflect.Value) error {
	if v.Kind() == reflect.String {
		v.SetString(string(val))
		return nil
	}
	return &UnmarshalTypeError{CBORType: t.String(), GoType: v.Type().String()}
}

func isImmutableKind(k reflect.Kind) bool {
	switch k {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64,
		reflect.String:
		return true
	default:
		return false
	}
}

func isHashableValue(rv reflect.Value) bool {
	switch rv.Kind() {
	case reflect.Slice, reflect.Map, reflect.Func:
		return false
	case reflect.Struct:
		switch rv.Type() {
		case typeTag:
			tag := rv.Interface().(Tag)
			return isHashableValue(reflect.ValueOf(tag.Content))
		case typeBigInt:
			return false
		}
	}
	return true
}

// convertByteSliceToByteString converts []byte to ByteString if
// - v is []byte type, or
// - v is Tag type and tag content type is []byte
// This function also handles nested tags.
// CBOR data is already verified to be well-formed before this function is used,
// so the recursion won't exceed max nested levels.
func convertByteSliceToByteString(v interface{}) (interface{}, bool) {
	switch v := v.(type) {
	case []byte:
		return ByteString(v), true
	case Tag:
		content, converted := convertByteSliceToByteString(v.Content)
		if converted {
			return Tag{Number: v.Number, Content: content}, true
		}
	}
	return v, false
}

// validBuiltinTag checks that supported built-in tag numbers are followed by expected content types.
func validBuiltinTag(tagNum uint64, contentHead byte) error {
	t := cborType(contentHead & 0xe0)
	switch tagNum {
	case 0:
		// Tag content (date/time text string in RFC 3339 format) must be string type.
		if t != cborTypeTextString {
			return errors.New("cbor: tag number 0 must be followed by text string, got " + t.String())
		}
		return nil
	case 1:
		// Tag content (epoch date/time) must be uint, int, or float type.
		if t != cborTypePositiveInt && t != cborTypeNegativeInt && (contentHead < 0xf9 || contentHead > 0xfb) {
			return errors.New("cbor: tag number 1 must be followed by integer or floating-point number, got " + t.String())
		}
		return nil
	case 2, 3:
		// Tag content (bignum) must be byte type.
		if t != cborTypeByteString {
			return errors.New("cbor: tag number 2 or 3 must be followed by byte string, got " + t.String())
		}
		return nil
	}
	return nil
}
