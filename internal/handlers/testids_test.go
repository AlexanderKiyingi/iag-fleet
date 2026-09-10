package handlers

import (
	"github.com/google/uuid"
)

// testID turns a readable label into a stable uuid.
//
// Migration 0043 retyped every surrogate id from TEXT to uuid, and these tests
// seeded literals like "VEH-DEL". Postgres rejects those on insert
// (22P02 invalid_text_representation), which is why 23 integration tests have
// failed since 0043 landed — a fixture problem, not a product one.
//
// The obvious fix, scattering uuid.NewString() through the seeds, would cost
// the thing that makes these tests readable: "VEH-DEL" says what the row is FOR
// at a glance, and a random uuid says nothing. It would also break every test
// that seeds a row under one name and then asserts on it under another, because
// two calls return two different ids.
//
// A deterministic v5 uuid keeps both properties. The label survives at the call
// site, and the same label always maps to the same uuid — within a test, across
// tests, and across runs — so seeding with testID("VEH-DEL") and deleting
// testID("VEH-DEL") still name one row.
//
// The namespace is arbitrary and fixed; only stability matters.
var testIDNamespace = uuid.MustParse("6f1c2a3e-4b5d-4e6f-8a9b-0c1d2e3f4a5b")

func testID(label string) string {
	return uuid.NewSHA1(testIDNamespace, []byte(label)).String()
}
