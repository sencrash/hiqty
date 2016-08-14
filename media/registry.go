package media

import (
	"errors"
	"os"
	"reflect"
)

// List of all registered services.
var Services map[string]ServiceFactory

// Adds a service to the global registry.
func AddService(id string, f ServiceFactory) {
	if Services == nil {
		Services = make(map[string]ServiceFactory)
	}
	Services[id] = f
}

// A ServiceFactory creates Services out of environment variables.
type ServiceFactory interface {
	// IsAvailable returns true if the necessary environment variables are set. This shouldn't read
	// much deeper into the values than that - leave validation to New().
	IsAvailable() bool

	// New creates a new instance of the described service. Returns an error if the service
	// couldn't be created for whatever reason, usually incorrectly set environment variables.
	New() (Service, error)
}

// The StandardFactory uses reflection to call a function with the values of a list of environment
// variables. This should cover the most common use case, hopefully.
type StandardFactory struct {
	// Environment variables required.
	Env []string

	// Function to call with the values.
	// func(a1[, a2, ...] string) (Service, error)
	NewFunc interface{}
}

// Available returns true if all requested environment variables are present.
func (f StandardFactory) IsAvailable() bool {
	for _, key := range f.Env {
		if _, ok := os.LookupEnv(key); !ok {
			return false
		}
	}
	return true
}

// New creates a new instance of the underlying service, by calling NewFunc with the resolved args.
func (f StandardFactory) New() (Service, error) {
	v := reflect.ValueOf(f.NewFunc)
	t := v.Type()
	if t.Kind() != reflect.Func {
		return nil, errors.New("NewFunc is not a function")
	}
	if t.NumOut() != 2 {
		return nil, errors.New("NewFunc has the wrong number of return values")
	}
	if !t.Out(0).Implements(reflect.TypeOf((*Service)(nil)).Elem()) ||
		!t.Out(1).Implements(reflect.TypeOf((*error)(nil)).Elem()) {
		return nil, errors.New("NewFunc has the wrong return types")
	}
	if t.NumIn() != len(f.Env) {
		return nil, errors.New("NewFunc has the wrong number of arguments")
	}

	args := make([]reflect.Value, len(f.Env))
	for i, key := range f.Env {
		args[i] = reflect.ValueOf(os.Getenv(key))
	}
	ret := v.Call(args)

	svc, _ := ret[0].Interface().(Service)
	err, _ := ret[1].Interface().(error)
	return svc, err
}
