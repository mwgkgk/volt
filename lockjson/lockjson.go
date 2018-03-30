package lockjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"

	"github.com/vim-volt/volt/logger"
	"github.com/vim-volt/volt/pathutil"
)

type ReposList []Repos
type ProfileList []Profile

type LockJSON struct {
	Version            int64       `json:"version"`
	CurrentProfileName string      `json:"current_profile_name"`
	Repos              ReposList   `json:"repos"`
	Profiles           ProfileList `json:"profiles"`
}

type ReposType string

const (
	ReposGitType    ReposType = "git"
	ReposStaticType ReposType = "static"
	ReposSystemType ReposType = "system"
)

type Repos struct {
	Type    ReposType          `json:"type"`
	Path    pathutil.ReposPath `json:"path"`
	Version string             `json:"version"`
}

type profReposPath []pathutil.ReposPath

type Profile struct {
	Name      string        `json:"name"`
	ReposPath profReposPath `json:"repos_path"`
}

const lockJSONVersion = 2

func initialLockJSON() *LockJSON {
	return &LockJSON{
		Version:            lockJSONVersion,
		CurrentProfileName: "default",
		Repos:              make([]Repos, 0),
		Profiles: []Profile{
			Profile{
				Name:      "default",
				ReposPath: make([]pathutil.ReposPath, 0),
			},
		},
	}
}

func Read() (*LockJSON, error) {
	return read(true)
}

func ReadNoMigrationMsg() (*LockJSON, error) {
	return read(false)
}

func read(doLog bool) (*LockJSON, error) {
	// Return initial lock.json struct if lockfile does not exist
	lockfile := pathutil.LockJSON()
	if !pathutil.Exists(lockfile) {
		return initialLockJSON(), nil
	}

	// Read lock.json
	bytes, err := ioutil.ReadFile(lockfile)
	if err != nil {
		return nil, err
	}
	var lockJSON LockJSON
	err = json.Unmarshal(bytes, &lockJSON)
	if err != nil {
		return nil, err
	}

	if lockJSON.Version < lockJSONVersion {
		if doLog {
			logger.Warnf("Performing auto-migration of lock.json: v%d -> v%d", lockJSON.Version, lockJSONVersion)
			logger.Warn("Please run 'volt migrate' to migrate explicitly if it's not updated by after operations")
		}
		err = migrate(bytes, &lockJSON)
		if err != nil {
			return nil, err
		}
	}

	// Validate lock.json
	err = validate(&lockJSON)
	if err != nil {
		return nil, errors.New("validation failed: lock.json: " + err.Error())
	}

	return &lockJSON, nil
}

func validate(lockJSON *LockJSON) error {
	if lockJSON.Version < 1 {
		return fmt.Errorf("lock.json version is '%d' (must be 1 or greater)", lockJSON.Version)
	}
	// Validate if volt can manipulate lock.json of this version
	if lockJSON.Version > lockJSONVersion {
		return fmt.Errorf("this lock.json version is '%d' which volt cannot recognize. please upgrade volt to process this file", lockJSON.Version)
	}

	// Validate if missing required keys exist
	err := validateMissing(lockJSON)
	if err != nil {
		return err
	}

	dup := make(map[string]bool, len(lockJSON.Repos))
	for i := range lockJSON.Repos {
		repos := &lockJSON.Repos[i]
		// Validate if repos[]/path is invalid format
		if _, err := pathutil.NormalizeRepos(repos.Path.String()); err != nil {
			return errors.New("'" + repos.Path.String() + "' is invalid repos path")
		}
		// Validate if duplicate repos[]/path exist
		if _, exists := dup[repos.Path.String()]; exists {
			return errors.New("duplicate repos '" + repos.Path.String() + "'")
		}
		dup[repos.Path.String()] = true
	}

	// Validate if duplicate profiles[]/name exist
	dup = make(map[string]bool, len(lockJSON.Profiles))
	for i := range lockJSON.Profiles {
		profile := &lockJSON.Profiles[i]
		if _, exists := dup[profile.Name]; exists {
			return errors.New("duplicate profile '" + profile.Name + "'")
		}
		dup[profile.Name] = true
	}

	for i := range lockJSON.Profiles {
		profile := &lockJSON.Profiles[i]
		dup = make(map[string]bool, len(lockJSON.Profiles)*10)
		for _, reposPath := range profile.ReposPath {
			// Validate if profiles[]/repos_path[] is invalid format
			if _, err := pathutil.NormalizeRepos(reposPath.String()); err != nil {
				return errors.New("'" + reposPath.String() + "' is invalid repos path")
			}
			// Validate if duplicate profiles[]/repos_path[] exist
			if _, exists := dup[reposPath.String()]; exists {
				return errors.New("duplicate '" + reposPath.String() + "' (repos_path) in profile '" + profile.Name + "'")
			}
			dup[reposPath.String()] = true
		}
	}

	// Validate if current_profile_name exists in profiles[]/name
	found := false
	for i := range lockJSON.Profiles {
		profile := &lockJSON.Profiles[i]
		if profile.Name == lockJSON.CurrentProfileName {
			found = true
			break
		}
	}
	if !found {
		return errors.New("'" + lockJSON.CurrentProfileName + "' (current_profile_name) doesn't exist in profiles")
	}

	// Validate if profiles[]/repos_path[] exists in repos[]/path
	reposMap := make(map[string]*Repos, len(lockJSON.Repos))
	for i := range lockJSON.Repos {
		reposMap[lockJSON.Repos[i].Path.String()] = &lockJSON.Repos[i]
	}
	for i := range lockJSON.Profiles {
		profile := &lockJSON.Profiles[i]
		for j, reposPath := range profile.ReposPath {
			if _, exists := reposMap[reposPath.String()]; !exists {
				return errors.New(
					"'" + reposPath.String() + "' (profiles[" + strconv.Itoa(i) +
						"].repos_path[" + strconv.Itoa(j) + "]) doesn't exist in repos")
			}
		}
	}

	return nil
}

func validateMissing(lockJSON *LockJSON) error {
	if lockJSON.Version == 0 {
		return errors.New("missing: version")
	}

	if lockJSON.Repos == nil {
		return errors.New("missing: repos")
	}
	for i := range lockJSON.Repos {
		repos := &lockJSON.Repos[i]
		if repos.Type == "" {
			return errors.New("missing: repos[" + strconv.Itoa(i) + "].type")
		}
		switch repos.Type {
		case ReposGitType:
			if repos.Version == "" {
				return errors.New("missing: repos[" + strconv.Itoa(i) + "].version")
			}
			fallthrough
		case ReposStaticType:
			if repos.Path.String() == "" {
				return errors.New("missing: repos[" + strconv.Itoa(i) + "].path")
			}
		default:
			return errors.New("repos[" + strconv.Itoa(i) + "].type is invalid type: " + string(repos.Type))
		}
	}
	if lockJSON.Profiles == nil {
		return errors.New("missing: profiles")
	}
	for i := range lockJSON.Profiles {
		profile := &lockJSON.Profiles[i]
		if profile.Name == "" {
			return errors.New("missing: profile[" + strconv.Itoa(i) + "].name")
		}
		if profile.ReposPath == nil {
			return errors.New("missing: profile[" + strconv.Itoa(i) + "].repos_path")
		}
		for j, reposPath := range profile.ReposPath {
			if reposPath.String() == "" {
				return errors.New("missing: profile[" + strconv.Itoa(i) + "].repos_path[" + strconv.Itoa(j) + "]")
			}
		}
	}
	return nil
}

func (lockJSON *LockJSON) Write() error {
	// Validate lock.json
	err := validate(lockJSON)
	if err != nil {
		return err
	}

	// Mkdir all if lock.json's directory does not exist
	lockfile := pathutil.LockJSON()
	if !pathutil.Exists(filepath.Dir(lockfile)) {
		err = os.MkdirAll(filepath.Dir(lockfile), 0755)
		if err != nil {
			return err
		}
	}

	// Write to lock.json
	bytes, err := json.MarshalIndent(lockJSON, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(pathutil.LockJSON(), bytes, 0644)
}

func (lockJSON *LockJSON) GetCurrentReposList() (ReposList, error) {
	// Find current profile
	profile, err := lockJSON.Profiles.FindByName(lockJSON.CurrentProfileName)
	if err != nil {
		// this must not be occurred because lockjson.Read()
		// validates that the matching profile exists
		return nil, err
	}

	reposList, err := lockJSON.GetReposListByProfile(profile)
	return reposList, err
}

func (profs *ProfileList) FindByName(name string) (*Profile, error) {
	for i := range *profs {
		if (*profs)[i].Name == name {
			return &(*profs)[i], nil
		}
	}
	return nil, errors.New("profile '" + name + "' does not exist")
}

func (profs *ProfileList) FindIndexByName(name string) int {
	for i := range *profs {
		if (*profs)[i].Name == name {
			return i
		}
	}
	return -1
}

func (profs *ProfileList) RemoveAllReposPath(reposPath pathutil.ReposPath) error {
	removed := false
	for i := range *profs {
		for j := 0; j < len((*profs)[i].ReposPath); {
			if (*profs)[i].ReposPath[j] == reposPath {
				(*profs)[i].ReposPath = append(
					(*profs)[i].ReposPath[:j],
					(*profs)[i].ReposPath[j+1:]...,
				)
				removed = true
				continue
			}
			j++
		}
	}
	if !removed {
		return errors.New("no matching profiles[]/repos_path[]: " + reposPath.String())
	}
	return nil
}

func (reposList *ReposList) Contains(reposPath pathutil.ReposPath) bool {
	_, err := reposList.FindByPath(reposPath)
	return err == nil
}

func (reposList *ReposList) FindByPath(reposPath pathutil.ReposPath) (*Repos, error) {
	for i := range *reposList {
		repos := &(*reposList)[i]
		if repos.Path == reposPath {
			return repos, nil
		}
	}
	return nil, errors.New("repos '" + reposPath.String() + "' does not exist")
}

func (reposList *ReposList) RemoveAllByPath(reposPath pathutil.ReposPath) error {
	for i := range *reposList {
		if (*reposList)[i].Path == reposPath {
			*reposList = append((*reposList)[:i], (*reposList)[i+1:]...)
			return nil
		}
	}
	return errors.New("no matching repos[]/path: " + reposPath.String())
}

func (reposPathList *profReposPath) Contains(reposPath pathutil.ReposPath) bool {
	return reposPathList.IndexOf(reposPath) >= 0
}

func (reposPathList *profReposPath) IndexOf(reposPath pathutil.ReposPath) int {
	for i := range *reposPathList {
		if (*reposPathList)[i] == reposPath {
			return i
		}
	}
	return -1
}

func (lockJSON *LockJSON) GetReposListByProfile(profile *Profile) (ReposList, error) {
	reposList := make(ReposList, 0, len(profile.ReposPath))
	for _, reposPath := range profile.ReposPath {
		repos, err := lockJSON.Repos.FindByPath(reposPath)
		if err != nil {
			return nil, err
		}
		reposList = append(reposList, *repos)
	}
	return reposList, nil
}
