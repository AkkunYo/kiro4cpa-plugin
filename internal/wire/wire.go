package wire

import "encoding/json"

type Envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *EnvelopeError  `json:"error,omitempty"`
}

type EnvelopeError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

// ErrorStatus carries an HTTP status so CPA can apply its cooldown and failover rules.
func ErrorStatus(code, message string, status int, retryable bool) []byte {
	out, _ := json.Marshal(Envelope{Error: &EnvelopeError{
		Code: code, Message: message, HTTPStatus: status, Retryable: retryable,
	}})
	return out
}

func OK(payload any) []byte {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Error("internal_marshal_error", err.Error())
	}
	env := Envelope{
		OK:     true,
		Result: raw,
	}
	out, _ := json.Marshal(env)
	return out
}

func Error(code, message string) []byte {
	env := Envelope{
		OK: false,
		Error: &EnvelopeError{
			Code:    code,
			Message: message,
		},
	}
	out, _ := json.Marshal(env)
	return out
}
