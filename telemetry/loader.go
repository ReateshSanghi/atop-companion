package telemetry

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
)

// Dataset holds the samples of every atop log found in one folder, one chronological series per host
type Dataset struct {
	Dir      string
	Files    int                 // atop logs that were decoded
	Skipped  int                 // records that could not be decoded
	Problems []string            // files that were not usable, with the reason
	Hosts    map[string][]Sample // host name -> samples, sorted by time, without duplicate timestamps
}

// HostNames returns the host names in the dataset, sorted
func (d *Dataset) HostNames() []string {
	names := make([]string, 0, len(d.Hosts))
	for name := range d.Hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// LoadFolder reads every atop raw log in dir. Files are recognised by their magic number, not their name, and
// anything else in the folder is ignored. Files are decoded in parallel. progress, if not nil, is called after
// each file with (done, total) and may be called from several goroutines.
func LoadFolder(dir string, progress func(done, total int)) (*Dataset, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	data := &Dataset{Dir: dir, Hosts: map[string][]Sample{}}
	var candidates []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if _, err := ReadAtopHeader(path); err != nil {
			continue // not an atop log: ignored silently
		}
		candidates = append(candidates, path)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no atop raw logs found in %s", dir)
	}

	type result struct {
		path string
		file *FileData
		err  error
	}
	jobs := make(chan string)
	results := make(chan result)
	var wg sync.WaitGroup
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				fd, err := ReadFile(path)
				results <- result{path: path, file: fd, err: err}
			}
		}()
	}
	go func() {
		for _, path := range candidates {
			jobs <- path
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	done := 0
	for r := range results {
		done++
		if r.err != nil {
			data.Problems = append(data.Problems, fmt.Sprintf("%s: %v", filepath.Base(r.path), r.err))
		} else {
			data.Files++
			data.Skipped += r.file.Skipped
			host := r.file.Header.Hostname
			if host == "" {
				host = "unknown"
			}
			data.Hosts[host] = append(data.Hosts[host], r.file.Samples...)
		}
		if progress != nil {
			progress(done, len(candidates))
		}
	}

	if data.Files == 0 {
		return nil, fmt.Errorf("none of the %d atop file(s) in %s could be read", len(candidates), dir)
	}
	for host, samples := range data.Hosts {
		data.Hosts[host] = sortAndDedupe(samples)
	}
	sort.Strings(data.Problems)
	return data, nil
}

// sortAndDedupe orders samples by time and keeps the first sample of each timestamp, so overlapping
// logs (for example a daily file and a weekly one) do not double count
func sortAndDedupe(samples []Sample) []Sample {
	sort.SliceStable(samples, func(i, j int) bool { return samples[i].Time.Before(samples[j].Time) })
	out := samples[:0]
	for i, s := range samples {
		if i == 0 || !s.Time.Equal(out[len(out)-1].Time) {
			out = append(out, s)
		}
	}
	return out
}
