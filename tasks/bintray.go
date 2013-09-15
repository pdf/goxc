package tasks

/*
   Copyright 2013 Am Laher

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/
//TODO: handle conflicts (delete or skip?)
//TODO: own options for downloadspage
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	//Tip for Forkers: please 'clone' from my url and then 'pull' from your url. That way you wont need to change the import path.
	//see https://groups.google.com/forum/?fromgroups=#!starred/golang-nuts/CY7o2aVNGZY
	"github.com/laher/goxc/core"
	"github.com/laher/goxc/typeutils"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const TASK_BINTRAY = "bintray"

//runs automatically
func init() {
	Register(Task{
		TASK_BINTRAY,
		"Upload artifacts to bintray.com, and generate a local markdown page of links (bintray registration details required in goxc config. See `goxc -h bintray`)",
		runTaskBintray,
		map[string]interface{}{"subject": "", "apikey": "", "repository": "",
			"apihost":       "https://api.bintray.com/",
			"downloadshost": "https://dl.bintray.com/",
			"downloadspage": "bintray.md",
			"fileheader":    "---\nlayout: default\ntitle: Downloads\n---\nFiles hosted at [bintray.com](https://bintray.com)\n\n",
			"include":       "*.zip,*.tar.gz,*.deb",
			"exclude":       "bintray.md"}})
}

func runTaskBintray(tp TaskParams) error {
	subject := tp.Settings.GetTaskSettingString(TASK_BINTRAY, "subject")
	apikey := tp.Settings.GetTaskSettingString(TASK_BINTRAY, "apikey")
	repository := tp.Settings.GetTaskSettingString(TASK_BINTRAY, "repository")
	pkg := tp.Settings.GetTaskSettingString(TASK_BINTRAY, "package")
	apiHost := tp.Settings.GetTaskSettingString(TASK_BINTRAY, "apihost")
	//downloadsHost := tp.Settings.GetTaskSettingString(TASK_BINTRAY, "downloadshost")
	versionDir := filepath.Join(tp.OutDestRoot, tp.Settings.GetFullVersionName())

	missing := []string{}

	if subject == "" {
		missing = append(missing, "subject")
	}
	if apikey == "" {
		missing = append(missing, "apikey")
	}
	if repository == "" {
		missing = append(missing, "repository")
	}
	if pkg == "" {
		missing = append(missing, "package")
	}
	if apiHost == "" {
		missing = append(missing, "apihost")
	}
	if len(missing) > 0 {
		return errors.New(fmt.Sprintf("bintray configuration missing (%v)", missing))
	}
	filename := tp.Settings.GetTaskSettingString(TASK_BINTRAY, "downloadspage")
	reportFilename := filepath.Join(tp.OutDestRoot, tp.Settings.GetFullVersionName(), filename)
	_, err := os.Stat(filepath.Dir(reportFilename))
	if err != nil {
		if os.IsNotExist(err) {
			return errors.New("No artifacts built for this version yet. Please build some artifacts before running the 'bintray' task")
		} else {
			return err
		}
	}
	flags := os.O_WRONLY | os.O_TRUNC | os.O_CREATE
	f, err := os.OpenFile(reportFilename, flags, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	fileheader := tp.Settings.GetTaskSettingString(TASK_BINTRAY, "fileheader")
	if fileheader != "" {
		_, err = fmt.Fprintf(f, "%s\n\n", fileheader)
	}
	_, err = fmt.Fprintf(f, "%s downloads (version %s)\n-------------\n", tp.AppName, tp.Settings.GetFullVersionName())
	if tp.Settings.IsVerbose() {
		log.Printf("Read directory %s", versionDir)
	}
	//for 'first entry in dir' detection.
	dirs := []string{}
	err = filepath.Walk(versionDir, func(path string, info os.FileInfo, e error) error {
		return walkFunc(path, info, e, f, dirs, tp)
	})
	if err != nil {
		return err
	}
	//close explicitly for return value
	return f.Close()
}

func walkFunc(fullPath string, fi2 os.FileInfo, err error, f *os.File, dirs []string, tp TaskParams) error {
	if fi2.IsDir() {
		return nil
	}
	subject := tp.Settings.GetTaskSettingString(TASK_BINTRAY, "subject")
	apikey := tp.Settings.GetTaskSettingString(TASK_BINTRAY, "apikey")
	repository := tp.Settings.GetTaskSettingString(TASK_BINTRAY, "repository")
	pkg := tp.Settings.GetTaskSettingString(TASK_BINTRAY, "package")
	apiHost := tp.Settings.GetTaskSettingString(TASK_BINTRAY, "apihost")
	downloadsHost := tp.Settings.GetTaskSettingString(TASK_BINTRAY, "downloadshost")
	includeResources := tp.Settings.GetTaskSettingString(TASK_BINTRAY, "include")
	excludeResources := tp.Settings.GetTaskSettingString(TASK_BINTRAY, "exclude")
	versionDir := filepath.Join(tp.OutDestRoot, tp.Settings.GetFullVersionName())

	relativePath := strings.Replace(fullPath, versionDir, "", -1)
	relativePath = strings.TrimPrefix(relativePath, "/")
	fmt.Printf("relative path %s, full path %s\n", relativePath, fullPath)

	resourceGlobs := core.ParseCommaGlobs(includeResources)
	//log.Printf("IncludeGlobs: %v", resourceGlobs)
	excludeGlobs := core.ParseCommaGlobs(excludeResources)
	//log.Printf("ExcludeGlobs: %v", excludeGlobs)
	matches := false
	for _, resourceGlob := range resourceGlobs {
		ok, err := filepath.Match(resourceGlob, fi2.Name())
		if err != nil {
			return err
		}
		if ok {
			matches = true
		}
	}
	if matches == false {
		log.Printf("Not included: %s (pattern %v)", relativePath, includeResources)
		return nil
	}
	for _, excludeGlob := range excludeGlobs {
		ok, err := filepath.Match(excludeGlob, fi2.Name())
		if err != nil {
			return err
		}
		if ok {
			log.Printf("Excluded: %s (pattern %v)", relativePath, excludeGlob)
			return nil
		}
	}
	first := true

	parent := filepath.Dir(relativePath)
	//platform := strings.Replace(parent, "_", "/", -1)
	//fmt.Fprintf(f, "\n * **%s**:", platform)
	for _, d := range dirs {
		if d == parent {
			first = false
		}
	}
	if first {
		dirs = append(dirs, parent)
	}
	//fmt.Printf("relative path %s, platform %s\n", relativePath, parent)

	text := strings.Replace(fi2.Name(), "_", "\\_", -1)
	if strings.HasSuffix(fi2.Name(), ".zip") {
		text = "zip"
	} else if strings.HasSuffix(fi2.Name(), ".deb") {
		text = "deb"
	} else if strings.HasSuffix(fi2.Name(), ".tar.gz") {
		text = "tar.gz"
	} else if fi2.Name() == tp.AppName || fi2.Name() == tp.AppName+".exe" {
		text = "executable"
	}
	url := apiHost + "/content/" + subject + "/" + repository + "/" + pkg + "/" + tp.Settings.GetFullVersionName() + "/" + relativePath
	// for some reason there's no /pkg/ level in the downloads url.
	downloadsUrl := downloadsHost + "/content/" + subject + "/" + repository + "/" + relativePath + "?direct"
	resp, err := uploadFile("PUT", url, subject, apikey, fullPath, relativePath)
	if err != nil {
		if serr, ok := err.(httpError); ok {
			if serr.statusCode == 409 {
				//conflict. skip
				//continue but dont publish.
				//TODO - provide an option to replace existing artifact
				//TODO - ?check exists before attempting upload?
				log.Printf("WARNING - file already exists. Skipping. %v", resp)
				return nil
			} else {
				return err
			}
		} else {
			return err
		}
	}

	log.Printf("File uploaded. (expected empty map[]): %v", resp)
	commaIfRequired := ""
	if first {
		first = false
	} else {
		commaIfRequired = ","
	}
	_, err = fmt.Fprintf(f, "%s [[%s](%s)]", commaIfRequired, text, downloadsUrl)
	if err != nil {
		return err
	}
	err = publish(apiHost, apikey, subject, repository, pkg, tp.Settings.GetFullVersionName())
	return err
}

func publish(apihost, apikey, subject, repository, pkg, version string) error {
	resp, err := doHttp("POST", apihost+"/content/"+subject+"/"+repository+"/"+pkg+"/"+version+"/publish", subject, apikey, nil, 0)
	if err == nil {
		log.Printf("File published. %v", resp)
	}
	return err
}

//PUT /content/:subject/:repo/:package/:version/:path
func uploadFile(method, url, subject, apikey, fullPath, relativePath string) (map[string]interface{}, error) {
	file, err := os.Open(fullPath)
	if err != nil {
		log.Printf("Error reading file for upload: %v", err)
		return nil, err
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil {
		log.Printf("Error statting file for upload: %v", err)
		return nil, err
	}
	resp, err := doHttp(method, url, subject, apikey, file, fi.Size())
	return resp, err
}

//NOTE: not necessary.
//POST /packages/:subject/:repo/:package/versions
func createVersion(apihost, apikey, subject, repository, pkg, version string) error {
	req := map[string]interface{}{"name": version, "release_notes": "built by goxc", "release_url": "http://x.x.x/x/x"}
	requestData, err := json.Marshal(req)
	if err != nil {
		return err
	}
	requestLength := len(requestData)
	reader := bytes.NewReader(requestData)
	resp, err := doHttp("POST", apihost+"/packages/"+subject+"/"+repository+"/"+pkg+"/versions", subject, apikey, reader, int64(requestLength))
	if err == nil {
		log.Printf("Created new version. %v", resp)
	}
	return err
}

type httpError struct {
	statusCode int
	message    string
}

func (e httpError) Error() string {
	return fmt.Sprintf("Error code: %d, message: %s", e.statusCode, e.message)
}

func doHttp(method, url, subject, apikey string, requestReader io.Reader, requestLength int64) (map[string]interface{}, error) {
	client := &http.Client{}
	req, err := http.NewRequest(method, url, requestReader)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(subject, apikey)
	if requestLength > 0 {
		log.Printf("Adding Header - Content-Length: %s", strconv.FormatInt(requestLength, 10))
		req.ContentLength = requestLength
	}
	//log.Printf("req: %v", req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	log.Printf("Http response received")
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()

	//200 is OK, 201 is Created, etc
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		log.Printf("Error code: %s", resp.Status)
		log.Printf("Error body: %s", body)
		return nil, httpError{resp.StatusCode, resp.Status}
	}
	log.Printf("Response status: '%s', Body: %s", resp.Status, body)
	var b map[string]interface{}
	if len(body) > 0 {
		err = json.Unmarshal(body, &b)
		if err != nil {
			return nil, err
		}
	}
	return b, err
}

func getVersions(apihost, apikey, subject, repository, pkg string) ([]string, error) {
	client := &http.Client{}
	url := apihost + "/packages/" + subject + "/" + repository + "/" + pkg
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(subject, apikey)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error calling %s - %v", url, err)
		return nil, err
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response body - %v", err)
		return nil, err
	}
	resp.Body.Close()
	var b map[string]interface{}
	err = json.Unmarshal(body, &b)
	if err != nil {
		log.Printf("Error parsing json body - %v", err)
		log.Printf("Body: %s", body)
		return nil, err
	}
	log.Printf("Body: %s", body)
	if versions, keyExists := b["versions"]; keyExists {
		versionsSlice, err := typeutils.ToStringSlice(versions, "versions")

		return versionsSlice, err
	}
	return nil, errors.New("Versions not listed!")
}

/*
// sample usage
func main() {
  target_url := "http://localhost:8888/"
  filename := "/path/to/file.rtf"
  postFile(filename, target_url)
}
*/
