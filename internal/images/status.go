package images

import (
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/flobilosaurus/agentnook/internal/agents"
	"github.com/flobilosaurus/agentnook/internal/config"
)

const NoManagedBuilds = "No managed builds. Build one with: nook images build NAME --agent NAME"

type CatalogStatus struct {
	ConfigFile string
	Images     []ImageStatus
}

type ImageStatus struct {
	Name, Agent, ArtifactState string
	Warning, Current, Default  bool
	Assignments                []ProjectAssignment
	detail                     []string
	agentID                    string
}

// ProjectAssignment is a saved choice for one agent, not a default for new projects.
type ProjectAssignment struct {
	Path, Agent, Profile, AgentDefault string
	Active, CurrentFolder              bool
}

// DescribeStatus reads catalog metadata and checks local artifact presence. It
// does not boot or inspect a VM.
func DescribeStatus(c config.Config, configFile, currentPath string, catalog agents.Catalog) CatalogStatus {
	status := CatalogStatus{ConfigFile: configFile}
	apt := strings.Join(strings.Fields(agents.ImagePackages), ", ")
	for _, entry := range c.Images {
		item := ImageStatus{
			Name: entry.Name, Agent: catalog.Label(entry.VM.Agent) + " " + entry.VM.AgentVersion,
			agentID: entry.VM.Agent,
			Default: strings.EqualFold(c.DefaultImages[entry.VM.Agent], entry.Name), ArtifactState: "present",
		}
		artifact := "present"
		if !Available(entry) {
			item.ArtifactState, item.Warning = "missing", true
			artifact = "missing or invalid"
		} else if entry.Architecture != runtime.GOARCH {
			item.ArtifactState, item.Warning = "wrong arch", true
			artifact = "present; host architecture is " + runtime.GOARCH
		}
		item.detail = []string{
			"Agent: " + item.Agent,
			fmt.Sprintf("Linux base: %s (%s)", entry.BaseImage, entry.Architecture),
			"Built: " + entry.BuiltAt,
			fmt.Sprintf("Artifact: %s (%s)", entry.VM.Image, artifact),
			fmt.Sprintf("Launch preset: %d CPU · %d MiB RAM · %d GiB disk", entry.VM.CPUs, entry.VM.Memory, entry.VM.Storage),
			"Build recipe apt packages (versions not recorded): " + apt,
		}
		if len(entry.Tools) == 0 {
			item.detail = append(item.detail, "Requested mise tools: none recorded")
		} else {
			item.detail = append(item.detail, "Requested mise tools (resolved versions not recorded):")
			for _, tool := range entry.Tools {
				item.detail = append(item.detail, "  - "+tool)
			}
		}
		status.Images = append(status.Images, item)
	}
	index := make(map[string]int, len(c.Images))
	for i, image := range c.Images {
		if _, exists := index[image.VM.Image]; !exists {
			index[image.VM.Image] = i
		}
	}
	aliases := make(map[string]int)
	for _, path := range slices.Sorted(maps.Keys(c.Projects)) {
		workspace := c.Projects[path]
		for agent, project := range workspace.Agents {
			ref := filepath.Clean(project.VM.Image)
			i, ok := index[ref]
			if !ok {
				i, ok = aliases[ref]
				if !ok {
					i = -1
					if image, found := FindByReference(c.Images, ref); found {
						i = index[image.VM.Image]
					}
					aliases[ref] = i
				}
			}
			if i < 0 || c.Images[i].VM.Agent != agent {
				continue
			}
			assignment := ProjectAssignment{
				Path: path, Agent: catalog.Label(agent), Profile: project.Profile,
				AgentDefault: c.DefaultImages[agent], Active: workspace.Agent == agent,
				CurrentFolder: path == currentPath,
			}
			item := &status.Images[i]
			item.Assignments = append(item.Assignments, assignment)
			item.Current = item.Current || (assignment.CurrentFolder && assignment.Active)
		}
	}
	// Keep the current folder's assignment next to its parent image in narrow panes.
	for i := range status.Images {
		assignments := status.Images[i].Assignments
		for j, assignment := range assignments {
			if assignment.CurrentFolder {
				copy(assignments[1:j+1], assignments[:j])
				assignments[0] = assignment
				break
			}
		}
	}
	return status
}

func (i ImageStatus) Title() string {
	title := i.Name
	if i.Default {
		title += " [default for " + i.agentID + "]"
	}
	if i.Current {
		title += " [current folder]"
	}
	return title
}

func (i ImageStatus) Details() []string { return i.detail }

func (s CatalogStatus) WriteText(out io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Config: %s\n", s.ConfigFile)
	if len(s.Images) == 0 {
		fmt.Fprintln(&b, NoManagedBuilds)
	} else {
		fmt.Fprintf(&b, "Managed images: %d (local catalog; no VM inspection)\n", len(s.Images))
	}
	for _, item := range s.Images {
		fmt.Fprintln(&b, "\n"+item.Title())
		for _, line := range item.Details() {
			fmt.Fprintln(&b, "  "+line)
		}
	}
	_, err := io.WriteString(out, b.String())
	return err
}
