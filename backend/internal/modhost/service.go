package modhost

import (
	"context"
	"fmt"
)

// Service is a module's background function, run by `serve` only.
type Service struct {
	Name string // the module name
	Run  func(ctx context.Context) error
}

var services []Service

func init() { onReset(func() { services = nil }) }

// RegisterService adds a background service. It panics after Freeze, on an
// invalid module name and on a nil Run.
func RegisterService(s Service) {
	register("service: Register", func() {
		switch {
		case !ValidName(s.Name):
			panic(fmt.Sprintf("service: invalid module name %q", s.Name))
		case isNil(s.Run):
			panic(fmt.Sprintf("service: module %q has a nil run function", s.Name))
		}
		services = append(services, s)
	})
}

// Services returns the registered services in registration order.
func Services() []Service {
	mu.Lock()
	defer mu.Unlock()
	return append([]Service(nil), services...)
}
