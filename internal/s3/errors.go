package s3

import (
	"encoding/xml"
	"errors"
	"net/http"

	"github.com/tildoc/tildoc/internal/core"
)

// Error is an S3 protocol error.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errAccessDenied(msg string) *Error {
	return &Error{Status: 403, Code: "AccessDenied", Message: msg}
}

func errAuthHeaderMalformed() *Error {
	return &Error{Status: 400, Code: "AuthorizationHeaderMalformed", Message: "the authorization header is malformed"}
}

func errInvalidArg(msg string) *Error {
	return &Error{Status: 400, Code: "InvalidArgument", Message: msg}
}

func errNoSuchKey() *Error {
	return &Error{Status: 404, Code: "NoSuchKey", Message: "the specified key does not exist"}
}

func errNoSuchBucket() *Error {
	return &Error{Status: 404, Code: "NoSuchBucket", Message: "the specified bucket does not exist"}
}

func errNoSuchUpload() *Error {
	return &Error{Status: 404, Code: "NoSuchUpload", Message: "the specified multipart upload does not exist"}
}

func errMalformedXML() *Error {
	return &Error{Status: 400, Code: "MalformedXML", Message: "the XML you provided was not well-formed"}
}

func errNotImplemented() *Error {
	return &Error{Status: 501, Code: "NotImplemented", Message: "this operation is not implemented"}
}

type errorBody struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource,omitempty"`
	RequestID string   `xml:"RequestId"`
}

// toS3 maps any error to an S3 error. keyContext selects NoSuchKey vs NoSuchBucket.
func toS3(err error, keyContext bool) *Error {
	var se *Error
	if errors.As(err, &se) {
		return se
	}
	msg := err.Error()
	var ce *core.Error
	if errors.As(err, &ce) {
		msg = ce.Msg
	}
	switch core.KindOf(err) {
	case core.ErrNotFound:
		if keyContext {
			return errNoSuchKey()
		}
		return errNoSuchBucket()
	case core.ErrForbidden, core.ErrUnauthorized:
		return errAccessDenied(msg)
	case core.ErrInvalid:
		return errInvalidArg(msg)
	case core.ErrTooLarge:
		return &Error{Status: 400, Code: "EntityTooLarge", Message: msg}
	case core.ErrConflict, core.ErrIsFolder, core.ErrNotFolder, core.ErrNotEmpty:
		return &Error{Status: 409, Code: "OperationAborted", Message: msg}
	}
	return &Error{Status: http.StatusInternalServerError, Code: "InternalError", Message: "we encountered an internal error"}
}
