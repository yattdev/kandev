package taskpreview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os/exec"
	"strconv"
	"time"
)

type Inspector interface {
	Validate(context.Context, Grant) error
}
type DockerInspector struct {
	Binary string
	Models bool // only the outer backend reads its existing exact model/claim paths
}

func (d DockerInspector) Validate(ctx context.Context, g Grant) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	format := `{"id":{{json .Id}},"image":{{json .Image}},"running":{{json .State.Running}},"labels":{"com.docker.compose.project":{{json (index .Config.Labels "com.docker.compose.project")}},"com.docker.compose.service":{{json (index .Config.Labels "com.docker.compose.service")}}},"ports":{{json .NetworkSettings.Ports}}}`
	command := exec.CommandContext(ctx, d.Binary, "inspect", "--type", "container", "--format", format, g.ContainerID)
	command.Env = []string{"PATH=/usr/bin:/bin", "DOCKER_HOST=unix:///var/run/docker.sock", "DOCKER_CONFIG=/run/kandev-preview-empty-docker-config"}
	b, err := command.Output()
	if err != nil || len(b) > 16384 {
		return ErrDenied
	}
	if err := ValidateInspection(b, g); err != nil {
		return err
	}
	if d.Models {
		return validateModel(g)
	}
	return nil
}
func ValidateInspection(b []byte, g Grant) error {
	var record struct {
		ID      string            `json:"id"`
		Image   string            `json:"image"`
		Running bool              `json:"running"`
		Labels  map[string]string `json:"labels"`
		Ports   map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"ports"`
	}
	if json.Unmarshal(b, &record) != nil || record.ID != g.ContainerID || record.Image != g.ImageID || !record.Running ||
		record.Labels["com.docker.compose.project"] != g.Project || record.Labels["com.docker.compose.service"] != g.Service {
		return ErrDenied
	}
	ports := record.Ports[strconv.Itoa(g.ContainerPort)+"/tcp"]
	if len(record.Ports) != 1 || len(ports) != 1 || ports[0].HostIP != "127.0.0.1" || ports[0].HostPort != strconv.Itoa(g.HostPort) {
		return ErrDenied
	}
	return nil
}
func boundedFile(path string) ([]byte, error) {
	f, err := privateFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || fi.Size() > 1048576 {
		return nil, ErrDenied
	}
	return io.ReadAll(io.LimitReader(f, 1048577))
}
func validateModel(g Grant) error {
	b, err := boundedFile(g.ModelPath)
	if err != nil {
		return ErrDenied
	}
	hash := sha256.Sum256(b)
	if hex.EncodeToString(hash[:]) != g.ModelSHA256 {
		return ErrDenied
	}
	for _, path := range []string{g.ModelPath, g.ClaimPath} {
		b, err = boundedFile(path)
		var record struct {
			TaskRoot string `json:"task_root"`
			CWD      string `json:"cwd"`
			Project  string `json:"project"`
		}
		if err != nil || json.Unmarshal(b, &record) != nil || record.TaskRoot != g.TaskRoot || record.CWD != g.CWD || record.Project != g.Project {
			return ErrDenied
		}
	}
	return nil
}
