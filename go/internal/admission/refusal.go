package admission

import (
	"errors"
	"strings"
)

// ForbiddenMessage is the message of a router operation the policy refused.
// A refusal that raised or joined a first-use question names it after the
// sentence, as rights:not_granted rights:question_pending:<id> (RIGHTS-FLOW
// ruling 2, inference INF-W11), so the caller can wait for the person's
// answer and call again.
func ForbiddenMessage(err error) string {
	const sentence = "router operation not permitted"
	var detail interface{ PermissionRefusalReason() string }
	if !errors.As(err, &detail) {
		return sentence
	}
	words := strings.Fields(detail.PermissionRefusalReason())
	if len(words) == 0 || words[0] != "rights:not_granted" {
		return sentence
	}
	return sentence + " " + strings.Join(words, " ")
}
