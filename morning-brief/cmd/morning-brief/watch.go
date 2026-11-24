package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
	"github.com/Barben360/hermes-image/morning-brief/internal/config"
	"github.com/Barben360/hermes-image/morning-brief/internal/source/cronjobs"
)

const watchPrefix = "⚠️ Le brief du matin n'a pas été livré : "

// watchCmd is the liveness watcher: silent when the job delivered today's brief, else one line.
// It always exits 0; the line is the alert, and every doubt prints it.
func watchCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cm common
	cm.register(fs)
	job := fs.String("job", "morning-brief", "name of the Hermes cron job to check")
	nowFlag := fs.String("now", "", "RFC3339 time to use instead of the clock")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	loc := location()
	now := time.Now().In(loc)
	if *nowFlag != "" {
		t, err := time.Parse(time.RFC3339, *nowFlag)
		if err != nil {
			fmt.Fprintf(stderr, "bad --now: %v\n", err)
			return 2
		}
		now = t.In(loc)
	}
	loaded := config.Load(cm.config, brief.Sections())
	secrets := brief.NewSecrets(cm.secretsDir)
	// Register every credential value so that no text printed below can carry one.
	if ents, err := os.ReadDir(cm.secretsDir); err == nil {
		for _, e := range ents {
			if !e.IsDir() {
				_, _ = secrets.Read(e.Name())
			}
		}
	}
	reason := judge(loaded.Sources.CronJobs, *job, now, loc)
	if reason != "" {
		fmt.Fprintln(stdout, watchPrefix+brief.Sanitize(secrets.Scrub(reason)))
	}
	return 0
}

// text makes a third-party string safe before it joins a reason.
func text(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// judge returns "" when the job delivered today's run, else the reason.
func judge(path, name string, now time.Time, loc *time.Location) string {
	if path == "" {
		return "chemin de jobs.json non configuré"
	}
	jobs, err := cronjobs.Load(path)
	if err != nil {
		return "jobs.json illisible : " + err.Error()
	}
	var j *cronjobs.Job
	for i := range jobs {
		if jobs[i].Name == name {
			j = &jobs[i]
			break
		}
	}
	if j == nil {
		return "job " + name + " introuvable dans jobs.json"
	}
	if !j.Enabled || (j.State != "" && j.State != "scheduled" && j.State != "running") {
		return fmt.Sprintf("job désactivé ou en pause (état %s)", j.State)
	}
	if j.LastRunAt == nil || *j.LastRunAt == "" {
		return "le job n'a jamais été lancé"
	}
	last, err := time.Parse(time.RFC3339Nano, *j.LastRunAt)
	if err != nil {
		return "date du dernier lancement illisible"
	}
	last = last.In(loc)
	y1, m1, d1 := last.Date()
	y2, m2, d2 := now.Date()
	if y1 != y2 || m1 != m2 || d1 != d2 {
		return "pas lancé aujourd'hui (dernier lancement " + last.Format("2006-01-02 15:04") + ")"
	}
	if last.Hour() < 7 {
		return "le dernier lancement date d'avant 07:00 (" + last.Format("15:04") + "), pas celui du matin"
	}
	if j.LastStatus == nil {
		return "statut du dernier lancement absent"
	}
	if *j.LastStatus != "ok" {
		r := "statut " + *j.LastStatus
		if e := text(j.LastError); e != "" {
			r += " : " + e
		}
		return r
	}
	if e := text(j.LastDeliveryError); e != "" {
		return "échec de la livraison : " + e
	}
	if j.Unverified() {
		return "livraison non vérifiée"
	}
	return ""
}
