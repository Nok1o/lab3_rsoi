package usecase

import "fmt"

type DependencyError struct {
	Service string
	Cause   error
}

func (err *DependencyError) Error() string {
	return fmt.Sprintf("%s request failed: %v", err.Service, err.Cause)
}

func (err *DependencyError) Unwrap() error {
	return err.Cause
}
