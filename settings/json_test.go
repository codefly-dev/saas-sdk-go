package settings_test

import (
	"testing"

	"github.com/codefly-dev/saas-sdk-go/settings"

	"google.golang.org/protobuf/types/descriptorpb"
)

func TestJSONCodecRoundTripsNestedPresenceUsingProtoNames(t *testing.T) {
	codec := settings.MustJSONCodec(
		func() *descriptorpb.FileDescriptorProto { return &descriptorpb.FileDescriptorProto{} },
		1024,
	)
	message := &descriptorpb.FileDescriptorProto{}
	noErr(t, fileSettings.GoPackage.Set(message, "example/service"))
	noErr(t, fileSettings.JavaMultipleFiles.Set(message, false))

	encoded, err := codec.Marshal(message)
	noErr(t, err)
	jsonEq(t, string(encoded), `{
		"options": {
			"go_package": "example/service",
			"java_multiple_files": false
		}
	}`)

	decoded, err := codec.Unmarshal(encoded)
	noErr(t, err)
	value, present, err := fileSettings.JavaMultipleFiles.Lookup(decoded)
	noErr(t, err)
	isTrue(t, present, "explicit false round-trips as present")
	isFalse(t, value, "value stays false")
}

func TestJSONCodecTreatsEmptyStorageAsEmptyMessage(t *testing.T) {
	codec := settings.MustJSONCodec(
		func() *descriptorpb.FileDescriptorProto { return &descriptorpb.FileDescriptorProto{} },
		1024,
	)

	for _, encoded := range [][]byte{nil, {}, []byte(`{}`)} {
		decoded, err := codec.Unmarshal(encoded)
		noErr(t, err)
		notNil(t, decoded, "decoded message is never nil")
		isNil(t, decoded.Options, "empty storage yields no parents")
	}
}

func TestJSONCodecIgnoresRemovedUnknownFieldsOnRead(t *testing.T) {
	codec := settings.MustJSONCodec(
		func() *descriptorpb.FileDescriptorProto { return &descriptorpb.FileDescriptorProto{} },
		1024,
	)
	decoded, err := codec.Unmarshal([]byte(`{
		"name": "settings.proto",
		"removed_setting": true
	}`))
	noErr(t, err)
	eq(t, decoded.GetName(), "settings.proto")
}

func TestJSONCodecTreatsNullAsAbsentRatherThanAThirdScalarState(t *testing.T) {
	codec := settings.MustJSONCodec(
		func() *descriptorpb.FileDescriptorProto { return &descriptorpb.FileDescriptorProto{} },
		1024,
	)

	decoded, err := codec.Unmarshal([]byte(`{
		"options": {"go_package": null}
	}`))

	noErr(t, err)
	value, present, err := fileSettings.GoPackage.Lookup(decoded)
	noErr(t, err)
	isFalse(t, present, "null is absent, not present")
	eq(t, value, "")
	defaulted, err := fileSettings.GoPackage.Get(decoded)
	noErr(t, err)
	eq(t, defaulted, "example/default")
}

func TestJSONCodecTreatsNullAtEveryNestedLevelAsAbsence(t *testing.T) {
	codec := settings.MustJSONCodec(
		func() *descriptorpb.FileDescriptorProto { return &descriptorpb.FileDescriptorProto{} },
		1024,
	)
	tests := map[string]string{
		"outer parent null": `{"options":null}`,
		"inner parent null": `{"options":{"features":null}}`,
		"leaf null":         `{"options":{"features":{"field_presence":null}}}`,
	}
	for name, encoded := range tests {
		t.Run(name, func(t *testing.T) {
			decoded, err := codec.Unmarshal([]byte(encoded))
			noErr(t, err)
			_, present, err := fileSettings.FieldPresence.Lookup(decoded)
			noErr(t, err)
			isFalse(t, present, "null at any level is absent")
			value, err := fileSettings.FieldPresence.Get(decoded)
			noErr(t, err)
			eq(t, value, descriptorpb.FeatureSet_EXPLICIT)
		})
	}
}

func TestJSONCodecPreservesExplicitScalarZeroPresence(t *testing.T) {
	codec := settings.MustJSONCodec(
		func() *descriptorpb.FileDescriptorProto { return &descriptorpb.FileDescriptorProto{} },
		1024,
	)
	decoded, err := codec.Unmarshal([]byte(`{
		"options": {
			"go_package": "",
			"java_multiple_files": false,
			"features": {"field_presence": "FIELD_PRESENCE_UNKNOWN"}
		}
	}`))
	noErr(t, err)

	text, present, err := fileSettings.GoPackage.Lookup(decoded)
	noErr(t, err)
	isTrue(t, present, "explicit empty string is present")
	eq(t, text, "")
	flag, present, err := fileSettings.JavaMultipleFiles.Lookup(decoded)
	noErr(t, err)
	isTrue(t, present, "explicit false is present")
	isFalse(t, flag, "value stays false")
	enum, present, err := fileSettings.FieldPresence.Lookup(decoded)
	noErr(t, err)
	isTrue(t, present, "explicit zero enum is present")
	eq(t, enum, descriptorpb.FeatureSet_FIELD_PRESENCE_UNKNOWN)
}

func TestJSONCodecEmptyObjectParentIsPresentButLeafRemainsAbsent(t *testing.T) {
	codec := settings.MustJSONCodec(
		func() *descriptorpb.FileDescriptorProto { return &descriptorpb.FileDescriptorProto{} },
		1024,
	)
	decoded, err := codec.Unmarshal([]byte(`{"options":{}}`))
	noErr(t, err)
	notNil(t, decoded.Options, "empty object parent is present")

	_, present, err := fileSettings.GoPackage.Lookup(decoded)
	noErr(t, err)
	isFalse(t, present, "leaf remains absent")
	value, err := fileSettings.GoPackage.Get(decoded)
	noErr(t, err)
	eq(t, value, "example/default")
	notNil(t, decoded.Options, "Get must not rewrite or prune the document")
}

func TestJSONCodecRejectsMalformedAndTypeInvalidJSON(t *testing.T) {
	codec := settings.MustJSONCodec(
		func() *descriptorpb.FileDescriptorProto { return &descriptorpb.FileDescriptorProto{} },
		1024,
	)

	_, err := codec.Unmarshal([]byte(`{"options":`))
	errContains(t, err, "unmarshal protobuf settings JSON")
	_, err = codec.Unmarshal([]byte(`{"options":{"go_package":false}}`))
	errContains(t, err, "unmarshal protobuf settings JSON")
}

func TestJSONCodecRejectsOversizedReadsAndWrites(t *testing.T) {
	codec := settings.MustJSONCodec(
		func() *descriptorpb.FileDescriptorProto { return &descriptorpb.FileDescriptorProto{} },
		8,
	)
	_, err := codec.Unmarshal([]byte(`{"name":"too-large"}`))
	errContains(t, err, "maximum is 8")

	name := "too-large"
	_, err = codec.Marshal(&descriptorpb.FileDescriptorProto{Name: &name})
	errContains(t, err, "maximum is 8")
}

func TestJSONCodecRejectsInvalidFactoriesAndNilMessages(t *testing.T) {
	_, err := settings.NewJSONCodec[*descriptorpb.FileDescriptorProto](nil, 1024)
	if err == nil {
		t.Fatal("expected error for nil factory")
	}
	_, err = settings.NewJSONCodec(
		func() *descriptorpb.FileDescriptorProto { return nil },
		1024,
	)
	if err == nil {
		t.Fatal("expected error for factory returning nil")
	}

	codec := settings.MustJSONCodec(
		func() *descriptorpb.FileDescriptorProto { return &descriptorpb.FileDescriptorProto{} },
		1024,
	)
	var message *descriptorpb.FileDescriptorProto
	_, err = codec.Marshal(message)
	errIs(t, err, settings.ErrNilMessage)
}
