package github

import "testing"

func TestProviderAccessAppClientSelectsOnlyExactManagedRegistration(t *testing.T) {
	client := &AppClient{}
	service := &Service{appRegistrationRuntimes: map[string]*githubAppRuntime{
		"registration-1": {registrationID: "registration-1", source: DeploymentAppSourceManaged,
			appClient: client},
	}}
	selected, err := service.ProviderAccessAppClient("registration-1")
	if err != nil || selected != client {
		t.Fatalf("managed App client = %p, err = %v", selected, err)
	}
	if selected, err := service.ProviderAccessAppClient("foreign-registration"); err == nil || selected != nil {
		t.Fatalf("foreign App client = %p, err = %v", selected, err)
	}
	service.appRegistrationRuntimes["registration-1"].source = DeploymentAppSourceNone
	if selected, err := service.ProviderAccessAppClient("registration-1"); err == nil || selected != nil {
		t.Fatalf("unmanaged App client = %p, err = %v", selected, err)
	}
}
