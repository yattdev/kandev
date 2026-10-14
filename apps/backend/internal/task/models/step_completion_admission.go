package models

import "context"

// StepCompletionAdmission binds an automatic move to the signal that admitted it.
// The transition repository revalidates this identity in its write transaction.
type StepCompletionAdmission struct {
	TaskID    string
	SessionID string
	StepID    string
	TurnID    string
}

type stepCompletionAdmissionKey struct{}

// WithStepCompletionAdmission carries a source identity to its transition write.
func WithStepCompletionAdmission(ctx context.Context, admission StepCompletionAdmission) context.Context {
	return context.WithValue(ctx, stepCompletionAdmissionKey{}, admission)
}

// StepCompletionAdmissionFromContext reads the source identity bound by orchestration.
func StepCompletionAdmissionFromContext(ctx context.Context) (StepCompletionAdmission, bool) {
	admission, ok := ctx.Value(stepCompletionAdmissionKey{}).(StepCompletionAdmission)
	return admission, ok
}

// WithoutStepCompletionAdmission keeps a source admission out of destination lifecycle work.
func WithoutStepCompletionAdmission(ctx context.Context) context.Context {
	return context.WithValue(ctx, stepCompletionAdmissionKey{}, false)
}
