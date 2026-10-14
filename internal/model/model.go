package model

import "fmt"

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string        { return fmt.Sprintf("%s: %s", e.Code, e.Message) }
func Err(code, message string) *Error { return &Error{Code: code, Message: message} }
