package notify

import "reflect"

// funcPtr returns the underlying function-pointer value for a Backend,
// used by tests to compare which backend pickBackend selected. Lives in
// non-test code so the test file doesn't have to import reflect just
// for one helper.
func funcPtr(b Backend) uintptr {
	return reflect.ValueOf(b).Pointer()
}
