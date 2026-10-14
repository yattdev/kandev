package service

import (
	"github.com/kandev/kandev/internal/workflow/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPortableCompletionGateSurvivesRenamedImport(t *testing.T) {
	s := &Service{}
	step := s.stepFromPortable("wf", models.StepPortable{Name: "Renamed gate", CompletionGatePolicy: models.CompletionGatePolicyQA}, map[int]string{}, "")
	require.Equal(t, models.CompletionGatePolicyQA, step.CompletionGatePolicy)
}
