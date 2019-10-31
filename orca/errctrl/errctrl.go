package errctrl

import "github.com/pkg/errors"

func Annotate(err *error, message string) {
	if *err != nil {
		*err = errors.WithMessage(*err, message)
	}
}
