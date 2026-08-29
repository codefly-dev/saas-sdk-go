package settings_test

import (
	"testing"

	"github.com/codefly-dev/saas-sdk-go/settings"

	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/typepb"
)

var fileSettings = struct {
	GoPackage         settings.Field[*descriptorpb.FileDescriptorProto, string]
	JavaMultipleFiles settings.Field[*descriptorpb.FileDescriptorProto, bool]
	OptimizeFor       settings.Field[*descriptorpb.FileDescriptorProto, descriptorpb.FileOptions_OptimizeMode]
	FieldPresence     settings.Field[*descriptorpb.FileDescriptorProto, descriptorpb.FeatureSet_FieldPresence]
}{
	GoPackage: settings.MustString(
		&descriptorpb.FileDescriptorProto{},
		"options.go_package",
		"example/default",
	),
	JavaMultipleFiles: settings.MustBool(
		&descriptorpb.FileDescriptorProto{},
		"options.java_multiple_files",
		false,
	),
	OptimizeFor: settings.MustEnum(
		&descriptorpb.FileDescriptorProto{},
		"options.optimize_for",
		descriptorpb.FileOptions_SPEED,
	),
	FieldPresence: settings.MustEnum(
		&descriptorpb.FileDescriptorProto{},
		"options.features.field_presence",
		descriptorpb.FeatureSet_EXPLICIT,
	),
}

func TestNestedSetMaterializesMissingParents(t *testing.T) {
	message := &descriptorpb.FileDescriptorProto{}
	isNil(t, message.Options, "options starts absent")

	noErr(t, fileSettings.GoPackage.Set(message, "example/service"))

	notNil(t, message.Options, "options materialized")
	notNil(t, message.Options.GoPackage, "go_package materialized")
	eq(t, message.GetOptions().GetGoPackage(), "example/service")
	value, present, err := fileSettings.GoPackage.Lookup(message)
	noErr(t, err)
	isTrue(t, present, "field present after Set")
	eq(t, value, "example/service")
}

func TestMissingParentReturnsConfiguredDefaultWithoutMaterializing(t *testing.T) {
	message := &descriptorpb.FileDescriptorProto{}

	value, err := fileSettings.GoPackage.Get(message)
	noErr(t, err)
	eq(t, value, "example/default")
	eq(t, fileSettings.GoPackage.Default(), "example/default")
	isNil(t, message.Options, "a read must never materialize a parent")

	_, present, err := fileSettings.GoPackage.Lookup(message)
	noErr(t, err)
	isFalse(t, present, "absent field is not present")
}

func TestApplyDefaultMaterializesMissingParents(t *testing.T) {
	message := &descriptorpb.FileDescriptorProto{}

	changed, err := fileSettings.GoPackage.ApplyDefault(message)

	noErr(t, err)
	isTrue(t, changed, "ApplyDefault changed the message")
	notNil(t, message.Options, "options materialized")
	eq(t, message.Options.GetGoPackage(), "example/default")
}

func TestApplyDefaultAcrossEveryNestedParentStateIsIdempotent(t *testing.T) {
	tests := map[string]*descriptorpb.FileDescriptorProto{
		"all parents absent": {},
		"outer parent present": {
			Options: &descriptorpb.FileOptions{},
		},
		"all parents present": {
			Options: &descriptorpb.FileOptions{
				Features: &descriptorpb.FeatureSet{},
			},
		},
	}
	for name, message := range tests {
		t.Run(name, func(t *testing.T) {
			changed, err := fileSettings.FieldPresence.ApplyDefault(message)
			noErr(t, err)
			isTrue(t, changed, "ApplyDefault changed the message")
			notNil(t, message.Options, "options materialized")
			notNil(t, message.Options.Features, "features materialized")
			notNil(t, message.Options.Features.FieldPresence, "field_presence materialized")
			eq(t, message.Options.Features.GetFieldPresence(), descriptorpb.FeatureSet_EXPLICIT)

			changed, err = fileSettings.FieldPresence.ApplyDefault(message)
			noErr(t, err)
			isFalse(t, changed, "applying a default twice must be a no-op")
		})
	}
}

func TestApplyDefaultNeverOverwritesExplicitZeroValue(t *testing.T) {
	message := &descriptorpb.FileDescriptorProto{}
	noErr(t, fileSettings.JavaMultipleFiles.Set(message, false))

	changed, err := fileSettings.JavaMultipleFiles.ApplyDefault(message)

	noErr(t, err)
	isFalse(t, changed, "explicit zero is not overwritten")
	value, present, err := fileSettings.JavaMultipleFiles.Lookup(message)
	noErr(t, err)
	isTrue(t, present, "explicit zero stays present")
	isFalse(t, value, "value stays false")
}

func TestGetNeverCoalescesExplicitScalarZeroValues(t *testing.T) {
	message := &descriptorpb.FileDescriptorProto{}
	noErr(t, fileSettings.GoPackage.Set(message, ""))
	noErr(t, fileSettings.JavaMultipleFiles.Set(message, false))
	noErr(t, fileSettings.FieldPresence.Set(
		message,
		descriptorpb.FeatureSet_FIELD_PRESENCE_UNKNOWN,
	))

	text, present, err := fileSettings.GoPackage.Lookup(message)
	noErr(t, err)
	isTrue(t, present, "explicit empty string is present")
	eq(t, text, "")
	text, err = fileSettings.GoPackage.Get(message)
	noErr(t, err)
	eq(t, text, "")

	flag, present, err := fileSettings.JavaMultipleFiles.Lookup(message)
	noErr(t, err)
	isTrue(t, present, "explicit false is present")
	isFalse(t, flag, "value stays false")

	enum, present, err := fileSettings.FieldPresence.Lookup(message)
	noErr(t, err)
	isTrue(t, present, "explicit zero enum is present")
	eq(t, enum, descriptorpb.FeatureSet_FIELD_PRESENCE_UNKNOWN)
	enum, err = fileSettings.FieldPresence.Get(message)
	noErr(t, err)
	eq(t, enum, descriptorpb.FeatureSet_FIELD_PRESENCE_UNKNOWN)
}

func TestSetAndClearMaterializeAndPruneMultipleMissingParents(t *testing.T) {
	message := &descriptorpb.FileDescriptorProto{}

	noErr(t, fileSettings.FieldPresence.Set(message, descriptorpb.FeatureSet_IMPLICIT))
	notNil(t, message.Options, "options materialized")
	notNil(t, message.Options.Features, "features materialized")
	eq(t, message.Options.Features.GetFieldPresence(), descriptorpb.FeatureSet_IMPLICIT)

	noErr(t, fileSettings.FieldPresence.Clear(message))
	isNil(t, message.Options, "all empty parents in the path must be pruned")
}

func TestExplicitScalarDefaultPreservesPresence(t *testing.T) {
	message := &descriptorpb.FileDescriptorProto{}

	noErr(t, fileSettings.JavaMultipleFiles.Set(message, false))

	value, present, err := fileSettings.JavaMultipleFiles.Lookup(message)
	noErr(t, err)
	isTrue(t, present, "explicit false must not collapse into unset")
	isFalse(t, value, "value stays false")
}

func TestSiblingSetSurvivesClearAndFinalClearPrunesParent(t *testing.T) {
	message := &descriptorpb.FileDescriptorProto{}
	noErr(t, fileSettings.GoPackage.Set(message, "example/service"))
	noErr(t, fileSettings.JavaMultipleFiles.Set(message, true))

	noErr(t, fileSettings.GoPackage.Clear(message))
	notNil(t, message.Options, "parent still contains a sibling setting")
	isTrue(t, message.Options.GetJavaMultipleFiles(), "sibling survives")

	noErr(t, fileSettings.JavaMultipleFiles.Clear(message))
	isNil(t, message.Options, "empty parent must be pruned")
}

func TestClearMissingNestedPathIsANoOp(t *testing.T) {
	message := &descriptorpb.FileDescriptorProto{}
	noErr(t, fileSettings.GoPackage.Clear(message))
	isNil(t, message.Options, "clear of absent path leaves parent absent")
}

func TestClearIsIdempotentAcrossPartiallyMaterializedParents(t *testing.T) {
	message := &descriptorpb.FileDescriptorProto{
		Options: &descriptorpb.FileOptions{
			Features: &descriptorpb.FeatureSet{},
		},
	}

	noErr(t, fileSettings.FieldPresence.Clear(message))
	isNil(t, message.Options, "empty parents pruned")
	noErr(t, fileSettings.FieldPresence.Clear(message))
	isNil(t, message.Options, "second clear is a no-op")
}

func TestClearDoesNotPruneAParentContainingUnknownWireFields(t *testing.T) {
	message := &descriptorpb.FileDescriptorProto{
		Options: &descriptorpb.FileOptions{},
	}
	message.Options.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	noErr(t, fileSettings.GoPackage.Set(message, "example/service"))

	noErr(t, fileSettings.GoPackage.Clear(message))

	notNil(t, message.Options, "parent with unknown fields must survive")
	eq(t, string(message.Options.ProtoReflect().GetUnknown()), string([]byte{0xa0, 0x06, 0x01}))
}

func TestEnumSetRejectsUndefinedValues(t *testing.T) {
	message := &descriptorpb.FileDescriptorProto{}
	err := fileSettings.OptimizeFor.Set(message, descriptorpb.FileOptions_OptimizeMode(999))
	errContains(t, err, "not defined")
	isNil(t, message.Options, "rejected set must not materialize parents")
}

func TestEnumSetAndDefault(t *testing.T) {
	message := &descriptorpb.FileDescriptorProto{}
	value, err := fileSettings.OptimizeFor.Get(message)
	noErr(t, err)
	eq(t, value, descriptorpb.FileOptions_SPEED)

	noErr(t, fileSettings.OptimizeFor.Set(message, descriptorpb.FileOptions_CODE_SIZE))
	value, present, err := fileSettings.OptimizeFor.Lookup(message)
	noErr(t, err)
	isTrue(t, present, "field present after Set")
	eq(t, value, descriptorpb.FileOptions_CODE_SIZE)
}

func TestFailedNestedEnumSetRollsBackOnlyNewParents(t *testing.T) {
	javaPackage := "com.example"
	message := &descriptorpb.FileDescriptorProto{
		Options: &descriptorpb.FileOptions{
			JavaPackage: &javaPackage,
		},
	}

	err := fileSettings.FieldPresence.Set(
		message,
		descriptorpb.FeatureSet_FieldPresence(999),
	)

	errContains(t, err, "not defined")
	notNil(t, message.Options, "pre-existing outer parent must survive rollback")
	eq(t, message.Options.GetJavaPackage(), "com.example")
	isNil(t, message.Options.Features, "new empty inner parent must be rolled back")
}

func TestNilMessagesFailWithoutPanicking(t *testing.T) {
	var message *descriptorpb.FileDescriptorProto

	_, err := fileSettings.GoPackage.Get(message)
	errIs(t, err, settings.ErrNilMessage)
	_, _, err = fileSettings.GoPackage.Lookup(message)
	errIs(t, err, settings.ErrNilMessage)
	_, err = fileSettings.GoPackage.Has(message)
	errIs(t, err, settings.ErrNilMessage)
	_, err = fileSettings.GoPackage.ApplyDefault(message)
	errIs(t, err, settings.ErrNilMessage)
	errIs(t, fileSettings.GoPackage.Set(message, "value"), settings.ErrNilMessage)
	errIs(t, fileSettings.GoPackage.Clear(message), settings.ErrNilMessage)
}

func TestInvalidCatalogPathsFailAtInitialization(t *testing.T) {
	mustPanic(t, func() {
		settings.MustString(&descriptorpb.FileDescriptorProto{}, "options.missing", "")
	})
	mustPanic(t, func() {
		settings.MustString(&descriptorpb.FileDescriptorProto{}, "name.child", "")
	})
	mustPanic(t, func() {
		// name is a proto3 scalar without presence.
		settings.MustString(&typepb.Type{}, "name", "")
	})
	mustPanic(t, func() {
		settings.MustEnum(
			&descriptorpb.FileDescriptorProto{},
			"options.optimize_for",
			descriptorpb.FileOptions_OptimizeMode(999),
		)
	})
}

func TestErrNilMessageIsStable(t *testing.T) {
	errIs(t, settings.ErrNilMessage, settings.ErrNilMessage)
}
