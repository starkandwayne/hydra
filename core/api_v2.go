package core

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jhunt/go-log"

	"github.com/shieldproject/shield/core/vault"
	"github.com/shieldproject/shield/db"
	"github.com/shieldproject/shield/route"
	"github.com/shieldproject/shield/timespec"
	"github.com/shieldproject/shield/util"
)

type v2SystemArchive struct {
	UUID     string `json:"uuid"`
	Schedule string `json:"schedule"`
	TakenAt  int64  `json:"taken_at"`
	Expiry   int    `json:"expiry"`
	Size     int64  `json:"size"`
	OK       bool   `json:"ok"`
	Notes    string `json:"notes"`
}
type v2SystemTask struct {
	UUID        string           `json:"uuid"`
	Type        string           `json:"type"`
	Status      string           `json:"status"`
	Owner       string           `json:"owner"`
	RequestedAt int64            `json:"requested_at"`
	StartedAt   int64            `json:"started_at"`
	StoppedAt   int64            `json:"stopped_at"`
	OK          bool             `json:"ok"`
	Notes       string           `json:"notes"`
	Archive     *v2SystemArchive `json:"archive,omitempty"`
	Log         string           `json:"log"`

	JobUUID     string `json:"job_uuid"`
	TenantUUID  string `json:"tenant_uuid"`
	ArchiveUUID string `json:"archive_uuid"`
	StoreUUID   string `json:"store_uuid"`
	TargetUUID  string `json:"target_uuid"`
}
type v2SystemJob struct {
	UUID        string `json:"uuid"`
	Schedule    string `json:"schedule"`
	Compression string `json:"compression"`
	From        string `json:"from"`
	To          string `json:"to"`
	OK          bool   `json:"ok"`

	Store struct {
		UUID    string `json:"uuid"`
		Name    string `json:"name"`
		Summary string `json:"summary"`
		Plugin  string `json:"plugin"`
		Healthy bool   `json:"healthy"`
	} `json:"store"`

	Keep struct {
		N    int `json:"n"`
		Days int `json:"days"`
	} `json:"keep"`
}
type v2System struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Notes       string `json:"notes"`
	OK          bool   `json:"ok"`
	Compression string `json:"compression"`

	Jobs  []v2SystemJob  `json:"jobs"`
	Tasks []v2SystemTask `json:"tasks"`
}

type v2LocalTenant struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
	Role string `json:"role"`
}
type v2LocalUser struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Account string `json:"account"`
	SysRole string `json:"sysrole"`

	Tenants []v2LocalTenant `json:"tenants"`
}

func (c *Core) v2API() *route.Router {
	r := &route.Router{
		Debug: c.Config.Debug,
	}

	r.Dispatch("GET /v2/info", func(r *route.Request) { // {{{
		r.OK(c.info)
	})
	// }}}
	r.Dispatch("GET /v2/bearings", func(r *route.Request) { // {{{
		var out struct {
			/* Status of the internal SHIELD Vault. */
			Vault string `json:"vault"`

			/* Information about this SHIELD installation itself,
			   including its name, the MOTD, the UI theme color,
			   API and software versions, etc. */
			SHIELD interface{} `json:"shield"`

			/* The currently logged-in user. */
			User *db.User `json:"user"`

			/* Global storage systems */
			Stores []*db.Store `json:"stores"`

			/* Initial "seed" data for the web UI data layer.
			   This, combined with the stream of event data that
			   we get from the /v2/events web socket should
			   suffice, and mitigate polling. */
			Tenants map[string]Bearing `json:"tenants"`
		}
		out.SHIELD = c.info

		if user, err := c.db.GetUserForSession(r.SessionID()); err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve user information"))
			return

		} else if user != nil {
			out.User = user

			/* retrieve vault status */
			out.Vault, err = c.vault.StatusString()
			if err != nil {
				r.Fail(route.Oops(err, "Unable to retrieve vault status"))
				return
			}

			/* retrieve global stores */
			out.Stores, err = c.db.GetAllStores(&db.StoreFilter{ForTenant: db.GlobalTenantUUID})
			if err != nil {
				r.Fail(route.Oops(err, "Unable to retrieve global stores"))
				return
			}

			/* retrieve the memberships for this user */
			memberships, err := c.db.GetMembershipsForUser(user.UUID)
			if err != nil {
				r.Fail(route.Oops(err, "Unable to retrieve user membership information"))
				return
			}

			out.Tenants = make(map[string]Bearing)
			for _, m := range memberships {
				b, err := c.BearingFor(m)
				if err != nil {
					r.Fail(route.Oops(err, "Unable to retrieve user membership information"))
					return
				}
				out.Tenants[b.Tenant.UUID] = b
			}
		}

		r.OK(out)
	})
	// }}}
	r.Dispatch("GET /v2/health", func(r *route.Request) { // {{{
		//you must be logged into shield to access shield health
		if c.IsNotAuthenticated(r) {
			return
		}
		health, err := c.checkHealth()
		if err != nil {
			r.Fail(route.Oops(err, "Unable to check SHIELD health"))
			return
		}
		r.OK(health)
	})
	// }}}

	r.Dispatch("GET /v2/scheduler/status", func(r *route.Request) { // {{{
		if c.IsNotSystemEngineer(r) {
			return
		}

		type named struct {
			UUID string `json:"uuid"`
			Name string `json:"name"`
		}

		type job struct {
			UUID     string `json:"uuid"`
			Name     string `json:"name"`
			Schedule string `json:"schedule"`
		}

		type archive struct {
			UUID string `json:"uuid"`
			Size int64  `json:"size"`
		}

		type backlogStatus struct {
			Priority int    `json:"priority"`
			Position int    `json:"position"`
			TaskUUID string `json:"task_uuid"`

			Op    string `json:"op"`
			Agent string `json:"agent"`

			Tenant  *named   `json:"tenant,omitempty"`
			Store   *named   `json:"store,omitempty"`
			System  *named   `json:"system,omitempty"`
			Job     *job     `json:"job,omitempty"`
			Archive *archive `json:"archive,omitempty"`
		}

		type workerStatus struct {
			ID       int    `json:"id"`
			Idle     bool   `json:"idle"`
			TaskUUID string `json:"task_uuid"`
			LastSeen int    `json:"last_seen"`

			Op     string `json:"op"`
			Status string `json:"status"`
			Agent  string `json:"agent"`

			Tenant  *named   `json:"tenant,omitempty"`
			Store   *named   `json:"store,omitempty"`
			System  *named   `json:"system,omitempty"`
			Job     *job     `json:"job,omitempty"`
			Archive *archive `json:"archive,omitempty"`
		}

		type status struct {
			Backlog []backlogStatus `json:"backlog"`
			Workers []workerStatus  `json:"workers"`
		}

		ps := c.scheduler.Status()
		out := status{
			Backlog: make([]backlogStatus, len(ps.Backlog)),
			Workers: make([]workerStatus, len(ps.Workers)),
		}

		tenants := make(map[string]*db.Tenant)
		stores := make(map[string]*db.Store)
		systems := make(map[string]*db.Target)
		jobs := make(map[string]*db.Job)
		archives := make(map[string]*db.Archive)

		for i, x := range ps.Backlog {
			out.Backlog[i].Priority = x.Priority + 1
			out.Backlog[i].Position = x.Position
			out.Backlog[i].TaskUUID = x.TaskUUID

			if task, err := c.db.GetTask(x.TaskUUID); err == nil && task != nil {
				out.Backlog[i].Op = task.Op
				out.Backlog[i].Agent = task.Agent

				if task.JobUUID != "" {
					j, found := jobs[task.JobUUID]
					if !found {
						j, err = c.db.GetJob(task.JobUUID)
						if j != nil && err == nil {
							jobs[j.UUID] = j
							found = true
						}
					}
					if found {
						out.Backlog[i].Job = &job{
							UUID: j.UUID,
							Name: j.Name,
						}
					}
				}

				out.Backlog[i].Tenant = &named{Name: "SYSTEM"}
				if task.TenantUUID != "" {
					if task.TenantUUID == db.GlobalTenantUUID {
						out.Backlog[i].Tenant.Name = "GLOBAL"
					} else {
						t, found := tenants[task.TenantUUID]
						if !found {
							t, err = c.db.GetTenant(task.TenantUUID)
							if t != nil && err == nil {
								tenants[t.UUID] = t
								found = true
							}
						}
						if found {
							out.Backlog[i].Tenant.UUID = t.UUID
							out.Backlog[i].Tenant.Name = t.Name
						}
					}
				}

				if task.StoreUUID != "" {
					s, found := stores[task.StoreUUID]
					if !found {
						s, err = c.db.GetStore(task.StoreUUID)
						if s != nil && err == nil {
							stores[s.UUID] = s
							found = true
						}
					}
					if found {
						out.Backlog[i].Store = &named{
							UUID: s.UUID,
							Name: s.Name,
						}
					}
				}

				if task.TargetUUID != "" {
					t, found := systems[task.TargetUUID]
					if !found {
						t, err = c.db.GetTarget(task.TargetUUID)
						if t != nil && err == nil {
							systems[t.UUID] = t
							found = true
						}
					}
					if found {
						out.Backlog[i].System = &named{
							UUID: t.UUID,
							Name: t.Name,
						}
					}
				}

				if task.ArchiveUUID != "" {
					a, found := archives[task.ArchiveUUID]
					if !found {
						a, err = c.db.GetArchive(task.ArchiveUUID)
						if a != nil && err == nil {
							archives[a.UUID] = a
							found = true
						}
					}
					if found {
						out.Backlog[i].Archive = &archive{
							UUID: a.UUID,
							Size: a.Size,
						}
					}
				}
			}
		}

		for i, x := range ps.Workers {
			out.Workers[i].ID = x.ID
			out.Workers[i].Idle = x.Idle
			out.Workers[i].TaskUUID = x.TaskUUID
			out.Workers[i].LastSeen = x.LastSeen

			if x.TaskUUID == "" {
				continue
			}

			if task, err := c.db.GetTask(x.TaskUUID); err == nil && task != nil {
				out.Workers[i].Op = task.Op
				out.Workers[i].Status = task.Status
				out.Workers[i].Agent = task.Agent

				if task.JobUUID != "" {
					j, found := jobs[task.JobUUID]
					if !found {
						j, err = c.db.GetJob(task.JobUUID)
						if j != nil && err == nil {
							jobs[j.UUID] = j
							found = true
						}
					}
					if found {
						out.Workers[i].Job = &job{
							UUID: j.UUID,
							Name: j.Name,
						}
					}
				}

				out.Workers[i].Tenant = &named{Name: "SYSTEM"}
				if task.TenantUUID != "" {
					if task.TenantUUID == db.GlobalTenantUUID {
						out.Workers[i].Tenant.Name = "GLOBAL"
					} else {
						t, found := tenants[task.TenantUUID]
						if !found {
							t, err = c.db.GetTenant(task.TenantUUID)
							if t != nil && err == nil {
								tenants[t.UUID] = t
								found = true
							}
						}
						if found {
							out.Workers[i].Tenant.UUID = t.UUID
							out.Workers[i].Tenant.Name = t.Name
						}
					}
				}

				if task.StoreUUID != "" {
					s, found := stores[task.StoreUUID]
					if !found {
						s, err = c.db.GetStore(task.StoreUUID)
						if s != nil && err == nil {
							stores[s.UUID] = s
							found = true
						}
					}
					if found {
						out.Workers[i].Store = &named{
							UUID: s.UUID,
							Name: s.Name,
						}
					}
				}

				if task.TargetUUID != "" {
					t, found := systems[task.TargetUUID]
					if !found {
						t, err = c.db.GetTarget(task.TargetUUID)
						if t != nil && err == nil {
							systems[t.UUID] = t
							found = true
						}
					}
					if found {
						out.Workers[i].System = &named{
							UUID: t.UUID,
							Name: t.Name,
						}
					}
				}

				if task.ArchiveUUID != "" {
					a, found := archives[task.ArchiveUUID]
					if !found {
						a, err = c.db.GetArchive(task.ArchiveUUID)
						if a != nil && err == nil {
							archives[a.UUID] = a
							found = true
						}
					}
					if found {
						out.Workers[i].Archive = &archive{
							UUID: a.UUID,
							Size: a.Size,
						}
					}
				}
			}
		}

		r.OK(out)
	})
	// }}}
	r.Dispatch("GET /v2/mbus/status", func(r *route.Request) { // {{{
		if c.IsNotSystemEngineer(r) {
			return
		}

		r.OK(c.bus.DumpState())
	})
	// }}}

	r.Dispatch("GET /v2/events", func(r *route.Request) { // {{{
		//you must be logged into shield to access the event stream
		if c.IsNotAuthenticated(r) {
			return
		}

		user, err := c.AuthenticatedUser(r)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to configure your SHIELD events stream"))
			return
		}

		queues := []string{
			"user:" + user.UUID,
			"tenant:" + db.GlobalTenantUUID,
		}

		memberships, err := c.db.GetMembershipsForUser(user.UUID)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to configure your SHIELD events stream"))
			return
		}
		for _, membership := range memberships {
			queues = append(queues, "tenant:"+membership.TenantUUID)
		}

		if user.SysRole != "" {
			queues = append(queues, "admins")
		}

		socket := r.Upgrade(route.WebSocketSettings{
			WriteTimeout: time.Duration(c.Config.API.Websocket.WriteTimeout) * time.Second,
		})
		if socket == nil {
			return
		}

		log.Infof("registering message bus web client")
		ch, slot, err := c.bus.Register(queues)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to begin streaming SHIELD events"))
			return
		}
		log.Infof("registered with message bus as [id:%d]", slot)

		closeMeSoftly := func() { c.bus.Unregister(slot) }
		go socket.Discard(closeMeSoftly)

		pingInterval := time.Duration(c.Config.API.Websocket.PingInterval) * time.Second
		pingTimer := time.NewTimer(pingInterval)
	writeLoop:
		for {
			select {
			case event := <-ch:
				b, err := json.Marshal(event)
				if err != nil {
					log.Errorf("message bus web client [id:%d] failed to marshal JSON for websocket relay: %s", slot, err)
				} else {
					if done, err := socket.Write(b); done {
						log.Infof("message bus web client [id:%d] closed their end of the socket", slot)
						log.Infof("message bus web client [id:%d] shutting down", slot)
						closeMeSoftly()
						break writeLoop
					} else if err != nil {
						log.Errorf("message bus web client [id:%d] failed to write message to remote end: %s", slot, err)
						log.Errorf("message bus web client [id:%d] shutting down", slot)
						closeMeSoftly()
						err := socket.SendClose()
						if err != nil {
							log.Warnf("message bus web client [id:%d] failed to write close message")
						}
						break writeLoop
					}
				}

				if !pingTimer.Stop() {
					<-pingTimer.C
				}
			case <-pingTimer.C:
				if err := socket.Ping(); err != nil {
					log.Infof("message bus web client [id:%d] failed to write ping")
					closeMeSoftly()
					break writeLoop
				}
			}
			pingTimer.Reset(pingInterval)
		}

		pingTimer.Stop()
		log.Infof("message bus web client [id:%d] disconnected; unregistering...", slot)
		closeMeSoftly()
	})
	// }}}

	r.Dispatch("GET /v2/tasks", func(r *route.Request) { // {{{
		if c.IsNotSystemEngineer(r) {
			return
		}

		limit, err := strconv.Atoi(r.Param("limit", "30"))
		if err != nil || limit < 0 || limit > 30 {
			r.Fail(route.Bad(err, "Invalid limit parameter given"))
			return
		}

		// check to see if we're offseting task requests
		paginationDate, err := strconv.ParseInt(r.Param("before", "0"), 10, 64)
		if err != nil || paginationDate < 0 {
			r.Fail(route.Bad(err, "Invalid before parameter given"))
			return
		}

		tasks, err := c.db.GetAllTasks(
			&db.TaskFilter{
				UUID:          r.Param("uuid", ""),
				ExactMatch:    r.ParamIs("exact", "t"),
				SkipActive:    r.ParamIs("active", "f"),
				SkipInactive:  r.ParamIs("active", "t"),
				ForStatus:     r.Param("status", ""),
				ForTarget:     r.Param("target", ""),
				ForStore:      r.Param("store", ""),
				ForOp:         r.Param("type", ""),
				Limit:         limit,
				Before:        paginationDate,
				StartedAfter:  r.ParamDuration("started_after"),
				StoppedAfter:  r.ParamDuration("stopped_after"),
				StartedBefore: r.ParamDuration("started_before"),
				StoppedBefore: r.ParamDuration("stopped_before"),
			},
		)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve task information"))
			return
		}

		r.OK(tasks)
	})
	// }}}
	r.Dispatch("GET /v2/tasks/:uuid", func(r *route.Request) { // {{{
		if c.IsNotSystemEngineer(r) {
			return
		}

		task, err := c.db.GetTask(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve task information"))
			return
		}
		if task == nil || task.TenantUUID != db.GlobalTenantUUID {
			r.Fail(route.NotFound(err, "No such task"))
			return
		}
		r.OK(task)
	})
	// }}}
	r.Dispatch("DELETE /v2/tenants/:uuid/tasks/:uuid", func(r *route.Request) { // {{{
		if c.IsNotSystemEngineer(r) {
			return
		}

		task, err := c.db.GetTask(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve task information"))
			return
		}
		if task == nil || task.TenantUUID == db.GlobalTenantUUID {
			r.Fail(route.NotFound(err, "No such task"))
			return
		}

		if err := c.db.CancelTask(task.UUID, time.Now()); err != nil {
			r.Fail(route.Oops(err, "Unable to cancel task"))
			return
		}

		r.Success("Canceled task successfully")
	})
	// }}}

	r.Dispatch("GET /v2/tenants/:uuid/health", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}
		health, err := c.checkTenantHealth(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to check SHIELD health"))
			return
		}
		r.OK(health)
	})
	// }}}

	r.Dispatch("POST /v2/init", func(r *route.Request) { // {{{
		var in struct {
			Master string `json:"master"`
		}
		if !r.Payload(&in) {
			return
		}

		if r.Missing("master", in.Master) {
			return
		}

		log.Infof("%s: initializing the SHIELD Core...", r)
		status, err := c.vault.Status()
		if err != nil {
			r.Fail(route.Oops(err, "Unable to initialize the SHIELD Core"))
			return
		}
		if status != vault.Blank {
			r.Fail(route.Bad(nil, "this SHIELD Core has already been initialized"))
			return
		}
		fixedKey, err := c.vault.Initialize(c.CryptFile(), in.Master)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to initialize the SHIELD Core"))
			return
		}

		r.OK(
			struct {
				Response string `json:"response"`
				FixedKey string `json:"fixed_key"`
			}{
				"Successfully initialized the SHIELD Core",
				fixedKey,
			})
	})
	// }}}
	r.Dispatch("POST /v2/lock", func(r *route.Request) { // {{{
		if c.IsNotSystemEngineer(r) {
			return
		}

		status, err := c.vault.Status()
		if err != nil {
			r.Fail(route.Forbidden(err, "Unable to lock the SHIELD Core"))
			return
		}
		if status == vault.Blank {
			r.Fail(route.Bad(nil, "this SHIELD Core has not yet been initialized"))
			return
		}
		if err := c.vault.Seal(); err != nil {
			r.Fail(route.Oops(err, "Unable to lock the SHIELD Core"))
			return
		}

		c.bus.Send("lock-core", "", nil, "*")
		r.Success("Successfully locked the SHIELD Core")
	})
	// }}}
	r.Dispatch("POST /v2/unlock", func(r *route.Request) { // {{{
		var in struct {
			Master string `json:"master"`
		}
		if !r.Payload(&in) {
			return
		}

		if r.Missing("master", in.Master) {
			return
		}

		status, err := c.vault.Status()
		if err != nil {
			r.Fail(route.Forbidden(err, "Unable to unlock the SHIELD Core: an internal error has occurred"))
			return
		}
		if status == vault.Blank {
			r.Fail(route.Bad(nil, "Unable to unlock the SHIELD Core: this SHIELD Core has not yet been initialized"))
			return
		}
		if err := c.vault.Unseal(c.CryptFile(), in.Master); err != nil {
			if strings.Contains(err.Error(), "incorrect master password") {
				r.Fail(route.Forbidden(err, "Unable to unlock the SHIELD Core: incorrect master password"))
				return
			}
			r.Fail(route.Oops(err, "Unable to unlock the SHIELD Core: an internal error has occurred"))
			return
		}

		c.bus.Send("unlock-core", "", nil, "*")
		r.Success("Successfully unlocked the SHIELD Core")
	})
	// }}}
	r.Dispatch("POST /v2/rekey", func(r *route.Request) { // {{{
		var in struct {
			Current     string `json:"current"`
			New         string `json:"new"`
			RotateFixed bool   `json:"rotate_fixed_key"`
		}
		if !r.Payload(&in) {
			return
		}

		if r.Missing("current", in.Current, "new", in.New) {
			return
		}

		fixedKey, err := c.vault.Rekey(c.CryptFile(), in.Current, in.New, in.RotateFixed)
		if err != nil {
			if strings.Contains(err.Error(), "incorrect master password") {
				r.Fail(route.Oops(err, "Unable to rekey the SHIELD Core: incorrect (current) master password"))
				return
			}
			r.Fail(route.Oops(err, "Unable to rekey the SHIELD Core: an internal error has occurred"))
			return
		}

		r.OK(
			struct {
				Response string `json:"response"`
				FixedKey string `json:"fixed_key"`
			}{
				"Successfully rekeyed the SHIELD Core",
				fixedKey,
			})
	})
	// }}}

	r.Dispatch("POST /v2/ui/users", func(r *route.Request) { // {{{
		var in struct {
			Search string `json:"search"`
		}
		if !r.Payload(&in) {
			return
		}
		if len(in.Search) < 3 {
			r.OK([]string{})
			return
		}

		users, err := c.db.GetAllUsers(&db.UserFilter{
			Search:  in.Search,
			Backend: "local",
		})
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve users from the database."))
			return
		}
		r.OK(users)
	})
	// }}}
	r.Dispatch("POST /v2/ui/check/timespec", func(r *route.Request) { // {{{
		var in struct {
			Timespec string `json:"timespec"`
		}
		if !r.Payload(&in) {
			return
		}

		spec, err := timespec.Parse(in.Timespec)
		if err != nil {
			r.Fail(route.Bad(err, fmt.Sprintf("%s", err)))
			return
		}

		r.Success("%s", spec)
	})
	// }}}

	r.Dispatch("GET /v2/auth/providers", func(r *route.Request) { // {{{
		l := make([]AuthProviderConfig, 0)

		for _, auth := range c.providers {
			cfg := auth.Configuration(false)
			l = append(l, cfg)
		}
		r.OK(l)
	})
	// }}}
	r.Dispatch("GET /v2/auth/providers/:name", func(r *route.Request) { // {{{
		if c.IsNotSystemAdmin(r) {
			return
		}

		a, ok := c.providers[r.Args[1]]
		if !ok {
			r.Fail(route.NotFound(nil, "No such authentication provider"))
			return
		}
		r.OK(a.Configuration(true))
	})
	// }}}

	r.Dispatch("GET /v2/auth/local/users", func(r *route.Request) { // {{{
		if c.IsNotSystemManager(r) {
			return
		}

		limit, err := strconv.Atoi(r.Param("limit", "0"))
		if err != nil || limit < 0 {
			r.Fail(route.Bad(err, "Invalid limit parameter given"))
			return
		}

		l, err := c.db.GetAllUsers(&db.UserFilter{
			UUID:       r.Param("uuid", ""),
			Account:    r.Param("account", ""),
			SysRole:    r.Param("sysrole", ""),
			ExactMatch: r.ParamIs("exact", "t"),
			Backend:    "local",
			Limit:      limit,
		})
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve local users information"))
			return
		}

		users := make([]v2LocalUser, len(l))
		for i, user := range l {
			memberships, err := c.db.GetMembershipsForUser(user.UUID)
			if err != nil {
				log.Errorf("failed to retrieve tenant memberships for user %s@%s (uuid %s): %s",
					user.Account, user.Backend, user.UUID, err)
				r.Fail(route.Oops(err, "Unable to retrieve local users information"))
				return
			}

			users[i] = v2LocalUser{
				UUID:    user.UUID,
				Name:    user.Name,
				Account: user.Account,
				SysRole: user.SysRole,
				Tenants: make([]v2LocalTenant, len(memberships)),
			}
			for j, membership := range memberships {
				users[i].Tenants[j].UUID = membership.TenantUUID
				users[i].Tenants[j].Name = membership.TenantName
				users[i].Tenants[j].Role = membership.Role
			}
		}

		r.OK(users)
	})
	// }}}
	r.Dispatch("GET /v2/auth/local/users/:uuid", func(r *route.Request) { // {{{
		if c.IsNotSystemManager(r) {
			return
		}

		user, err := c.db.GetUserByID(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve local user information"))
			return
		}

		if user == nil {
			r.Fail(route.NotFound(nil, "user '%s' not found (for local auth provider)", r.Args[1]))
			return
		}

		memberships, err := c.db.GetMembershipsForUser(user.UUID)
		if err != nil {
			log.Errorf("failed to retrieve tenant memberships for user %s@%s (uuid %s): %s",
				user.Account, user.Backend, user.UUID, err)
			r.Fail(route.Oops(err, "Unable to retrieve local user information"))
			return
		}

		local_user := v2LocalUser{
			UUID:    user.UUID,
			Name:    user.Name,
			Account: user.Account,
			SysRole: user.SysRole,
			Tenants: make([]v2LocalTenant, len(memberships)),
		}

		for j, membership := range memberships {
			local_user.Tenants[j].UUID = membership.TenantUUID
			local_user.Tenants[j].Name = membership.TenantName
			local_user.Tenants[j].Role = membership.Role
		}

		r.OK(local_user)
	})
	// }}}
	r.Dispatch("POST /v2/auth/local/users", func(r *route.Request) { // {{{
		if c.IsNotSystemManager(r) {
			return
		}

		var in struct {
			UUID     string `json:"uuid"`
			Name     string `json:"name"`
			Account  string `json:"account"`
			Password string `json:"password"`
			SysRole  string `json:"sysrole"`
		}
		if !r.Payload(&in) {
			return
		}

		if r.Missing("name", in.Name, "account", in.Account, "password", in.Password) {
			return
		}

		if in.SysRole != "" {
			switch in.SysRole {
			case
				"admin",
				"manager",
				"engineer":
			default:
				r.Fail(route.Bad(nil, "System role '%s' is invalid", in.SysRole))
				return
			}
		}

		u := &db.User{
			UUID:    in.UUID,
			Name:    in.Name,
			Account: in.Account,
			Backend: "local",
			SysRole: in.SysRole,
		}
		u.SetPassword(in.Password)

		exists, err := c.db.GetUser(u.Account, "local")
		if err != nil {
			r.Fail(route.Oops(err, "Unable to create local user '%s'", in.Account))
			return
		}

		if exists != nil {
			r.Fail(route.Bad(nil, "user '%s' already exists", u.Account))
			return
		}

		u, err = c.db.CreateUser(u)
		if u == nil || err != nil {
			r.Fail(route.Oops(err, "Unable to create local user '%s'", in.Account))
			return
		}
		r.OK(u)
	})
	// }}}
	r.Dispatch("PATCH /v2/auth/local/users/:uuid", func(r *route.Request) { // {{{
		if c.IsNotSystemManager(r) {
			return
		}

		var in struct {
			Name     string `json:"name"`
			Password string `json:"password"`
			SysRole  string `json:"sysrole"`
		}
		if !r.Payload(&in) {
			return
		}

		user, err := c.db.GetUserByID(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to update local user '%s'", user.Account))
			return
		}
		if user == nil || user.Backend != "local" {
			r.Fail(route.NotFound(nil, "No such local user"))
			return
		}
		if in.Name != "" {
			user.Name = in.Name
		}

		if in.SysRole != "" {
			switch in.SysRole {
			case
				"admin",
				"manager",
				"engineer":
				user.SysRole = in.SysRole
			default:
				r.Fail(route.Bad(nil, "System role '%s' is invalid", in.SysRole))
				return
			}
		}

		if in.Password != "" {
			user.SetPassword(in.Password)
		}

		err = c.db.UpdateUser(user)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to update local user '%s'", user.Account))
			return
		}

		r.Success("Updated")
	})
	// }}}
	r.Dispatch("DELETE /v2/auth/local/users/:uuid", func(r *route.Request) { // {{{
		if c.IsNotSystemManager(r) {
			return
		}

		user, err := c.db.GetUserByID(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve local user information"))
			return
		}
		if user == nil || user.Backend != "local" {
			r.Fail(route.NotFound(nil, "Local User '%s' not found", r.Args[1]))
			return
		}

		err = c.db.DeleteUser(user)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to delete local user '%s' (%s)", r.Args[1], user.Account))
			return
		}
		r.Success("Successfully deleted local user")
	})
	// }}}

	r.Dispatch("GET /v2/auth/tokens", func(r *route.Request) { // {{{
		if c.IsNotAuthenticated(r) {
			return
		}

		user, _ := c.AuthenticatedUser(r)
		tokens, err := c.db.GetAllAuthTokens(&db.AuthTokenFilter{
			User: user,
		})
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve tokens information"))
			return
		}

		for i := range tokens {
			tokens[i].Session = ""
		}

		r.OK(tokens)
	})
	// }}}
	r.Dispatch("POST /v2/auth/tokens", func(r *route.Request) { // {{{
		if c.IsNotAuthenticated(r) {
			return
		}
		user, _ := c.AuthenticatedUser(r)

		var in struct {
			Name string `json:"name"`
		}
		if !r.Payload(&in) {
			return
		}
		if r.Missing("name", in.Name) {
			return
		}

		existing, err := c.db.GetAllAuthTokens(&db.AuthTokenFilter{
			Name: in.Name,
			User: user,
		})
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve tokens information"))
			return
		}
		if len(existing) != 0 {
			r.Fail(route.Bad(err, "A token with this name already exists"))
			return
		}

		token, id, err := c.db.GenerateAuthToken(in.Name, user)
		if id == "" || err != nil {
			r.Fail(route.Oops(err, "Unable to generate new token"))
			return
		}

		r.OK(token)
	})
	// }}}
	r.Dispatch("DELETE /v2/auth/tokens/:token", func(r *route.Request) { // {{{
		if c.IsNotAuthenticated(r) {
			return
		}

		user, _ := c.AuthenticatedUser(r)
		if err := c.db.DeleteAuthToken(r.Args[1], user); err != nil {
			r.Fail(route.Oops(err, "Unable to revoke auth token"))
			return
		}

		r.Success("Token revoked")
	})
	// }}}

	r.Dispatch("GET /v2/auth/sessions", func(r *route.Request) { // {{{
		if c.IsNotSystemAdmin(r) {
			return
		}

		limit, err := strconv.Atoi(r.Param("limit", "0"))
		if err != nil || limit < 0 {
			r.Fail(route.Bad(err, "Invalid limit parameter given"))
			return
		}

		sessions, err := c.db.GetAllSessions(
			&db.SessionFilter{
				UUID:       r.Param("uuid", ""),
				UserUUID:   r.Param("user_uuid", ""),
				Name:       r.Param("name", ""),
				IP:         r.Param("ip_addr", ""),
				ExactMatch: r.ParamIs("exact", "t"),
				IsToken:    r.ParamIs("is_token", "t"),
				Limit:      limit,
			},
		)
		for _, session := range sessions {
			if session.UUID == r.SessionID() {
				session.CurrentSession = true
				break
			}
		}

		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve session information"))
			return
		}

		r.OK(sessions)
	})
	// }}}
	r.Dispatch("GET /v2/auth/sessions/:uuid", func(r *route.Request) { // {{{
		if c.IsNotSystemAdmin(r) {
			return
		}

		limit, err := strconv.Atoi(r.Param("limit", "0"))
		if err != nil || limit < 0 {
			r.Fail(route.Bad(err, "Invalid limit parameter given"))
			return
		}

		session, err := c.db.GetSession(r.Args[1])
		if session == nil || err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve session information"))
			return
		}
		if session.UUID == r.SessionID() {
			session.CurrentSession = true
		}

		r.OK(session)
	})
	// }}}
	r.Dispatch("DELETE /v2/auth/sessions/:uuid", func(r *route.Request) { // {{{
		if c.IsNotSystemAdmin(r) {
			return
		}
		session, err := c.db.GetSession(r.Args[1])
		if session == nil || err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve session information"))
			return
		}
		if session == nil {
			r.Fail(route.NotFound(nil, "Session not found"))
			return
		}

		if err := c.db.ClearSession(session.UUID); err != nil {
			r.Fail(route.Oops(err, "Unable to clear session '%s' (%s)", r.Args[1], session.IP))
			return
		}
		r.Success("Successfully cleared session '%s' (%s)", r.Args[1], session.IP)
	})
	// }}}

	r.Dispatch("GET /v2/tenants/:uuid/systems", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		targets, err := c.db.GetAllTargets(
			&db.TargetFilter{
				SkipUsed:   r.ParamIs("unused", "t"),
				SkipUnused: r.ParamIs("unused", "f"),

				UUID:       r.Param("uuid", ""),
				SearchName: r.Param("name", ""),

				ForPlugin:  r.Param("plugin", ""),
				ExactMatch: r.ParamIs("exact", "t"),
				ForTenant:  r.Args[1],
			},
		)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve systems information"))
			return
		}

		systems := make([]v2System, len(targets))
		for i, target := range targets {
			err := c.v2copyTarget(&systems[i], target)
			if err != nil {
				r.Fail(route.Oops(err, "Unable to retrieve systems information"))
				return
			}
		}

		r.OK(systems)
	})
	// }}}
	r.Dispatch("GET /v2/tenants/:uuid/systems/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		target, err := c.db.GetTarget(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve system information"))
			return
		}

		if target == nil || target.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(err, "No such system"))
			return
		}

		var system v2System
		err = c.v2copyTarget(&system, target)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve system information"))
			return
		}

		// keep track of our archives, indexed by task UUID
		archives := make(map[string]*db.Archive)
		aa, err := c.db.GetAllArchives(
			&db.ArchiveFilter{
				ForTarget:  target.UUID,
				WithStatus: []string{"valid"},
			},
		)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve system information"))
			return
		}
		for _, archive := range aa {
			archives[archive.UUID] = archive
		}
		// check to see if we're offseting task requests
		paginationDate, err := strconv.ParseInt(r.Param("before", "0"), 10, 64)
		if err != nil || paginationDate < 0 {
			r.Fail(route.Bad(err, "Invalid before parameter given"))
			return
		}

		tasks, err := c.db.GetAllTasks(
			&db.TaskFilter{
				ForTarget:    target.UUID,
				OnlyRelevant: true,
				Before:       paginationDate,
				Limit:        30,
			},
		)

		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve system information"))
			return
		}

		//check if there's more tasks on the specific last date and append if so
		if len(tasks) > 0 {
			appendingtasks, err := c.db.GetAllTasks(
				&db.TaskFilter{
					ForTarget:    target.UUID,
					OnlyRelevant: true,
					RequestedAt:  tasks[len(tasks)-1].RequestedAt,
				},
			)
			if err != nil {
				r.Fail(route.Oops(err, "Unable to retrieve system information"))
				return
			}
			if (len(appendingtasks) > 1) && (tasks[len(tasks)-1].UUID != appendingtasks[len(appendingtasks)-1].UUID) {
				log.Infof("Got a misjointed request, need to merge these two arrays.")
				for i, task := range appendingtasks {
					if task.UUID == tasks[len(tasks)-1].UUID {
						tasks = append(tasks, appendingtasks[i+1:]...)
						break
					}
				}
			}
		}

		if !c.CanSeeCredentials(r, r.Args[1]) {
			c.db.RedactAllTaskLogs(tasks)
		}

		system.Tasks = make([]v2SystemTask, len(tasks))
		for i, task := range tasks {
			system.Tasks[i].UUID = task.UUID
			system.Tasks[i].Type = task.Op
			system.Tasks[i].Status = task.Status
			system.Tasks[i].Owner = task.Owner
			system.Tasks[i].OK = task.OK
			system.Tasks[i].Notes = task.Notes
			system.Tasks[i].RequestedAt = task.RequestedAt
			system.Tasks[i].StartedAt = task.StartedAt
			system.Tasks[i].StoppedAt = task.StoppedAt
			system.Tasks[i].Log = task.Log

			system.Tasks[i].JobUUID = task.JobUUID
			system.Tasks[i].TenantUUID = task.TenantUUID
			system.Tasks[i].StoreUUID = task.StoreUUID
			system.Tasks[i].ArchiveUUID = task.ArchiveUUID
			system.Tasks[i].TargetUUID = task.TargetUUID

			if archive, ok := archives[task.ArchiveUUID]; ok {
				system.Tasks[i].Archive = &v2SystemArchive{
					UUID:     archive.UUID,
					Schedule: archive.Job,
					Expiry:   (int)((archive.ExpiresAt - archive.TakenAt) / 86400),
					Notes:    archive.Notes,
					Size:     archive.Size,
				}
			}
		}

		r.OK(system)
	})
	// }}}
	r.Dispatch("GET /v2/tenants/:uuid/systems/:uuid/config", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		target, err := c.db.GetTarget(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve system information"))
			return
		}

		if target == nil || target.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(err, "No such system"))
			return
		}

		config, err := target.Configuration(c.db, c.CanSeeCredentials(r, target.TenantUUID))
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve system information"))
			return
		}

		r.OK(config)
	})
	// }}}
	r.Dispatch("POST /v2/tenants/:uuid/systems", func(r *route.Request) { // {{{
		if c.IsNotTenantEngineer(r, r.Args[1]) {
			return
		}

		var in struct {
			Target struct {
				UUID        string `json:"uuid"`
				Name        string `json:"name"`
				Summary     string `json:"summary"`
				Plugin      string `json:"plugin"`
				Agent       string `json:"agent"`
				Compression string `json:"compression"`

				Config map[string]interface{} `json:"config"`
			} `json:"target"`

			Store struct {
				UUID      string `json:"uuid"`
				Name      string `json:"name"`
				Summary   string `json:"summary"`
				Plugin    string `json:"plugin"`
				Agent     string `json:"agent"`
				Threshold int64  `json:"threshold"`

				Config map[string]interface{} `json:"config"`
			} `json:"store"`

			Job struct {
				Name     string `json:"name"`
				Schedule string `json:"schedule"`
				KeepDays int    `json:"keep_days"`
				FixedKey bool   `json:"fixed_key"`
				Paused   bool   `json:"paused"`
				Retries  int    `json:"retries"`

				KeepN int
			} `json:"job"`
		}
		if !r.Payload(&in) {
			return
		}

		sched, err := timespec.Parse(in.Job.Schedule)
		if err != nil {
			r.Fail(route.Oops(err, "Invalid or malformed SHIELD Job Schedule '%s'", in.Job.Schedule))
			return
		}

		if in.Job.KeepDays < 0 {
			r.Fail(route.Oops(nil, "Invalid or malformed SHIELD Job Archive Retention Period '%dd'", in.Job.KeepDays))
			return
		}
		if in.Job.KeepDays < c.Config.Limit.Retention.Min {
			r.Fail(route.Oops(nil, "SHIELD Job Archive Retention Period '%dd' is too short, archives must be kept for a minimum of %d days", in.Job.KeepDays, c.Config.Limit.Retention.Min))
			return
		}
		if in.Job.KeepDays > c.Config.Limit.Retention.Max {
			r.Fail(route.Oops(nil, "SHIELD Job Archive Retention Period '%dd' is too long, archives may be kept for a maximum of %d days", in.Job.KeepDays, c.Config.Limit.Retention.Max))
			return
		}
		in.Job.KeepN = sched.KeepN(in.Job.KeepDays)

		if in.Target.Compression == "" {
			in.Target.Compression = DefaultCompressionType
		}

		var (
			target *db.Target
			store  *db.Store
		)

		if in.Target.UUID != "" {
			target, err = c.db.GetTarget(in.Target.UUID)
			if err != nil {
				r.Fail(route.Oops(err, "Unable to retrieve system information"))
				return
			}
			if target == nil || target.TenantUUID != r.Args[1] {
				r.Fail(route.NotFound(nil, "No such system"))
				return
			}

		} else {
			target, err = c.db.CreateTarget(&db.Target{
				TenantUUID:  r.Args[1],
				Name:        in.Target.Name,
				Summary:     in.Target.Summary,
				Plugin:      in.Target.Plugin,
				Config:      in.Target.Config,
				Agent:       in.Target.Agent,
				Compression: in.Target.Compression,
				Healthy:     true,
			})
			if target == nil || err != nil {
				r.Fail(route.Oops(err, "Unable to create new data target"))
				return
			}
		}

		if in.Store.UUID != "" {
			store, err = c.db.GetStore(in.Store.UUID)
			if err != nil {
				r.Fail(route.Oops(err, "Unable to retrieve cloud storage information"))
				return
			}
			if store == nil || (!store.Global && store.TenantUUID != r.Args[1]) {
				r.Fail(route.NotFound(nil, "No such store"))
				return
			}

		} else {
			store, err = c.db.CreateStore(&db.Store{
				TenantUUID: r.Args[1],
				Name:       in.Store.Name,
				Summary:    in.Store.Summary,
				Agent:      in.Store.Agent,
				Plugin:     in.Store.Plugin,
				Config:     in.Store.Config,
				Threshold:  in.Store.Threshold,
				Healthy:    true, /* let's be optimistic */
			})
			if store == nil || err != nil {
				r.Fail(route.Oops(err, "Unable to create new storage system"))
				return
			}

			if _, err := c.db.CreateTestStoreTask("system", store); err != nil {
				log.Errorf("failed to schedule storage test task (non-critical) for %s (%s): %s",
					store.Name, store.UUID, err)
			}
		}

		job, err := c.db.CreateJob(&db.Job{
			TenantUUID: r.Args[1],
			Name:       in.Job.Name,
			Schedule:   in.Job.Schedule,
			KeepN:      in.Job.KeepN,
			KeepDays:   in.Job.KeepDays,
			Retries:    in.Job.Retries,
			Paused:     in.Job.Paused,
			StoreUUID:  store.UUID,
			TargetUUID: target.UUID,
			FixedKey:   in.Job.FixedKey,
		})
		if job == nil || err != nil {
			r.Fail(route.Oops(err, "Unable to create new job"))
			return
		}

		r.OK(target)
	})
	// }}}
	r.Dispatch("PATCH /v2/tenants/:uuid/systems/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantEngineer(r, r.Args[1]) {
			return
		}

		var in struct {
			Annotations []struct {
				Type        string `json:"type"`
				UUID        string `json:"uuid"`
				Disposition string `json:"disposition"`
				Notes       string `json:"notes"`
				Clear       string `json:"clear"`
			} `json:"annotations"`
		}
		if !r.Payload(&in) {
			return
		}

		target, err := c.db.GetTarget(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve system information"))
			return
		}

		if target == nil || target.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "No such system"))
			return
		}

		for _, ann := range in.Annotations {
			switch ann.Type {
			case "task":
				err = c.db.AnnotateTargetTask(
					target.UUID,
					ann.UUID,
					&db.TaskAnnotation{
						Disposition: ann.Disposition,
						Notes:       ann.Notes,
						Clear:       ann.Clear,
					},
				)
				if err != nil {
					r.Fail(route.Oops(err, "Unable to annotate task %s", ann.UUID))
					return
				}

			case "archive":
				err = c.db.AnnotateTargetArchive(
					target.UUID,
					ann.UUID,
					ann.Notes,
				)
				if err != nil {
					r.Fail(route.Oops(err, "Unable to annotate archive %s", ann.UUID))
					return
				}

			default:
				r.Fail(route.Bad(nil, "unrecognized system annotation type '%s'", ann.Type))
				return
			}
		}

		_ = c.db.MarkTasksIrrelevant()
		r.Success("annotated successfully")
	})
	// }}}
	r.Dispatch("DELETE /v2/tenants/:uuid/systems/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantEngineer(r, r.Args[1]) {
			return
		}

		/* FIXME */
		r.Fail(route.Errorf(501, nil, "%s: not implemented", r))
	})
	// }}}

	r.Dispatch("GET /v2/agents", func(r *route.Request) { // {{{
		if c.IsNotSystemAdmin(r) {
			return
		}

		agents, err := c.db.GetAllAgents(&db.AgentFilter{
			UUID:        r.Param("uuid", ""),
			ExactMatch:  r.ParamIs("exact", "t"),
			SkipHidden:  r.ParamIs("hidden", "f"),
			SkipVisible: r.ParamIs("hidden", "t"),
		})
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve agent information"))
			return
		}

		resp := struct {
			Agents   []*db.Agent         `json:"agents"`
			Problems map[string][]string `json:"problems"`
		}{
			Agents:   agents,
			Problems: make(map[string][]string),
		}

		for _, agent := range agents {
			id := agent.UUID
			pp := make([]string, 0)

			if agent.Version == "" {
				pp = append(pp, Problems["legacy-shield-agent-version"])
			}
			if agent.Version == "dev" {
				pp = append(pp, Problems["dev-shield-agent-version"])
			}

			resp.Problems[id] = pp
		}
		r.OK(resp)
	})
	// }}}
	r.Dispatch("GET /v2/agents/:uuid", func(r *route.Request) { // {{{
		if c.IsNotSystemAdmin(r) {
			return
		}

		agent, err := c.db.GetAgent(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve agent information"))
			return
		}
		if agent == nil {
			r.Fail(route.NotFound(nil, "No such agent"))
			return
		}

		raw, err := agent.Metadata()
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve agent information"))
			return
		}

		resp := struct {
			Agent    db.Agent               `json:"agent"`
			Metadata map[string]interface{} `json:"metadata"`
			Problems []string               `json:"problems"`
		}{
			Agent:    *agent,
			Metadata: raw,
			Problems: make([]string, 0),
		}

		if agent.Version == "" {
			resp.Problems = append(resp.Problems, Problems["legacy-shield-agent-version"])
		}
		if agent.Version == "dev" {
			resp.Problems = append(resp.Problems, Problems["dev-shield-agent-version"])
		}

		r.OK(resp)
	})
	// }}}
	r.Dispatch("DELETE /v2/agents/:uuid", func(r *route.Request) { // {{{
		if c.IsNotSystemAdmin(r) {
			return
		}

		agent, err := c.db.GetAgent(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve agent information"))
			return
		}
		if agent == nil {
			r.Fail(route.NotFound(nil, "No such agent"))
			return
		}

		err = c.db.DeleteAgent(agent)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to delete agent"))
			return
		}

		r.Success("deleted agent %s (at %s)", agent.Name, agent.Address)
	})
	// }}}
	r.Dispatch("POST /v2/agents", func(r *route.Request) { // {{{
		var in struct {
			Name string `json:"name"`
			Port int    `json:"port"`
		}
		if !r.Payload(&in) {
			return
		}

		peer := regexp.MustCompile(`[,:].*`).ReplaceAllString(r.Req.Header.Get("X-Forwarded-For"), "")
		if peer == "" {
			peer = regexp.MustCompile(`:\d+$`).ReplaceAllString(r.Req.RemoteAddr, "")
			if peer == "" {
				r.Fail(route.Oops(nil, "Unable to determine remote peer address from '%s'", r.Req.RemoteAddr))
				return
			}
		}

		if in.Name == "" {
			r.Fail(route.Bad(nil, "No `name' provided with pre-registration request"))
			return
		}
		if in.Port == 0 {
			r.Fail(route.Bad(nil, "No `port' provided with pre-registration request"))
			return
		}

		err := c.db.PreRegisterAgent(peer, in.Name, in.Port)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to pre-register agent %s at %s:%d", in.Name, peer, in.Port))
			return
		}
		r.Success("pre-registered agent %s at %s:%d", in.Name, peer, in.Port)
	})
	// }}}
	r.Dispatch("POST /v2/agents/:uuid/(show|hide)", func(r *route.Request) { // {{{
		if c.IsNotSystemAdmin(r) {
			return
		}

		agent, err := c.db.GetAgent(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve agent information"))
			return
		}
		if agent == nil {
			r.Fail(route.NotFound(nil, "No such agent"))
			return
		}

		agent.Hidden = (r.Args[2] == "hide")
		if err := c.db.UpdateAgent(agent); err != nil {
			r.Fail(route.Oops(err, "Unable to set agent visibility"))
			return
		}

		if agent.Hidden {
			r.Success("Agent is now visible only to SHIELD site engineers")
		} else {
			r.Success("Agent is now visible to everyone")
		}
	})
	// }}}
	r.Dispatch("POST /v2/agents/:uuid/resync", func(r *route.Request) { // {{{
		if c.IsNotSystemAdmin(r) {
			return
		}

		agent, err := c.db.GetAgent(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve agent information"))
			return
		}
		if agent == nil {
			r.Fail(route.NotFound(nil, "No such agent"))
			return
		}

		c.ScheduleAgentStatusCheckTasks(&db.AgentFilter{UUID: agent.UUID})
		r.Success("Ad hoc agent resynchronization underway")
	})
	// }}}

	r.Dispatch("GET /v2/tenants", func(r *route.Request) { // {{{
		if c.IsNotSystemManager(r) {
			return
		}

		limit, err := strconv.Atoi(r.Param("limit", "0"))
		if err != nil || limit < 0 {
			r.Fail(route.Bad(err, "Invalid limit parameter given"))
			return
		}

		tenants, err := c.db.GetAllTenants(&db.TenantFilter{
			UUID:       r.Param("uuid", ""),
			Name:       r.Param("name", ""),
			ExactMatch: r.ParamIs("exact", "t"),
			Limit:      limit,
		})

		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve tenants information"))
			return
		}
		r.OK(tenants)
	})
	// }}}
	r.Dispatch("GET /v2/tenants/:uuid", func(r *route.Request) { // {{{
		if !c.CanManageTenants(r, r.Args[1]) {
			return
		}

		tenant, err := c.db.GetTenant(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve tenant information"))
			return
		}
		if tenant == nil {
			r.Fail(route.NotFound(nil, "No such tenant"))
			return
		}

		tenant.Members, err = c.db.GetUsersForTenant(tenant.UUID)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve tenant memberships information"))
			return
		}

		r.OK(tenant)
	})
	// }}}
	r.Dispatch("POST /v2/tenants", func(r *route.Request) { // {{{
		if c.IsNotSystemManager(r) {
			return
		}

		var in struct {
			UUID string `json:"uuid"`
			Name string `json:"name"`

			Users []struct {
				UUID    string `json:"uuid"`
				Account string `json:"account"`
				Role    string `json:"role"`
			} `json:"users"`
		}
		if !r.Payload(&in) {
			return
		}

		if r.Missing("name", in.Name) {
			return
		}

		if strings.ToLower(in.Name) == "system" {
			r.Fail(route.Bad(nil, "tenant name 'system' is reserved"))
			return
		}

		t, err := c.db.CreateTenant(&db.Tenant{
			UUID: in.UUID,
			Name: in.Name,
		})
		if t == nil || err != nil {
			r.Fail(route.Oops(err, "Unable to create new tenant '%s'", in.Name))
			return
		}

		for _, u := range in.Users {
			user, err := c.db.GetUserByID(u.UUID)
			if err != nil {
				r.Fail(route.Oops(err, "Unrecognized user account '%s'", user))
				return
			}

			if user == nil {
				r.Fail(route.Oops(err, "Unrecognized user account '%s'", user))
				return
			}

			if user.Backend != "local" {
				r.Fail(route.Oops(nil, "Unable to invite '%s@%s' to tenant '%s' - only local users can be invited.", user.Account, user.Backend, t.Name))
				return
			}

			err = c.db.AddUserToTenant(u.UUID, t.UUID, u.Role)
			if err != nil {
				r.Fail(route.Oops(err, "Unable to invite '%s' to tenant '%s'", user.Account, t.Name))
				return
			}
		}

		r.OK(t)
	})
	// }}}
	r.Dispatch("POST /v2/tenants/:uuid/invite", func(r *route.Request) { // {{{
		if !c.CanManageTenants(r, r.Args[1]) {
			return
		}

		tenant, err := c.db.GetTenant(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to update tenant memberships information"))
			return
		}
		if tenant == nil {
			r.Fail(route.NotFound(nil, "No such tenant"))
			return
		}

		var in struct {
			Users []struct {
				UUID    string `json:"uuid"`
				Account string `json:"account"`
				Role    string `json:"role"`
			} `json:"users"`
		}
		if !r.Payload(&in) {
			return
		}

		for _, u := range in.Users {
			user, err := c.db.GetUserByID(u.UUID)
			if err != nil {
				r.Fail(route.Oops(err, "Unrecognized user account '%s'", user))
				return
			}

			if user == nil {
				r.Fail(route.Oops(err, "Unrecognized user account '%s'", user))
				return
			}

			if user.Backend != "local" {
				r.Fail(route.Oops(nil, "Unable to invite '%s@%s' to tenant '%s' - only local users can be invited.", user.Account, user.Backend, tenant.Name))
				return
			}

			err = c.db.AddUserToTenant(u.UUID, tenant.UUID, u.Role)
			if err != nil {
				r.Fail(route.Oops(err, "Unable to invite '%s' to tenant '%s'", user.Account, tenant.Name))
				return
			}
		}

		r.Success("Invitations sent")
	})
	// }}}
	r.Dispatch("POST /v2/tenants/:uuid/banish", func(r *route.Request) { // {{{
		if !c.CanManageTenants(r, r.Args[1]) {
			return
		}

		tenant, err := c.db.GetTenant(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to update tenant memberships information"))
			return
		}
		if tenant == nil {
			r.Fail(route.NotFound(nil, "No such tenant"))
			return
		}

		var in struct {
			Users []struct {
				UUID    string `json:"uuid"`
				Account string `json:"account"`
			} `json:"users"`
		}
		if !r.Payload(&in) {
			return
		}

		for _, u := range in.Users {
			user, err := c.db.GetUserByID(u.UUID)
			if err != nil {
				r.Fail(route.Oops(err, "Unrecognized user account '%s'", user))
				return
			}

			if user == nil {
				r.Fail(route.Oops(err, "Unrecognized user account '%s'", user))
				return
			}

			if user.Backend != "local" {
				r.Fail(route.Oops(nil, "Unable to banish '%s@%s' from tenant '%s' - only local users can be banished.", user.Account, user.Backend, tenant.Name))
				return
			}

			err = c.db.RemoveUserFromTenant(u.UUID, tenant.UUID)
			if err != nil {
				r.Fail(route.Oops(err, "Unable to banish '%s' from tenant '%s'", user.Account, tenant.Name))
				return
			}
		}

		r.Success("Banishments served.")
	})
	// }}}
	r.Dispatch("GET /v2/tenants/:uuid", func(r *route.Request) { // {{{
		if c.IsNotSystemManager(r) {
			return
		}

		tenant, err := c.db.GetTenant(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve tenant information"))
			return
		}
		if tenant == nil {
			r.Fail(route.NotFound(nil, "No such tenant"))
			return
		}

		tenant.Members, err = c.db.GetUsersForTenant(tenant.UUID)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve tenant memberships information"))
			return
		}

		r.OK(tenant)
	})
	// }}}
	r.Dispatch("PATCH /v2/tenants/:uuid", func(r *route.Request) { // {{{
		if c.IsNotSystemManager(r) {
			return
		}

		var in struct {
			Name string `json:"name"`
		}
		if !r.Payload(&in) {
			return
		}

		if r.Missing("name", in.Name) {
			return
		}

		tenant, err := c.db.GetTenant(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve tenant information"))
			return
		}
		if tenant == nil {
			r.Fail(route.NotFound(err, "No such tenant"))
			return
		}

		if in.Name != "" {
			tenant.Name = in.Name
		}

		t, err := c.db.UpdateTenant(tenant)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to update tenant '%s'", in.Name))
			return
		}
		r.OK(t)
	})
	// }}}
	r.Dispatch("DELETE /v2/tenants/:uuid", func(r *route.Request) { // {{{
		if c.IsNotSystemManager(r) {
			return
		}

		tenant, err := c.db.GetTenant(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve tenant information"))
			return
		}

		if tenant == nil {
			r.Fail(route.NotFound(nil, "Tenant not found"))
			return
		}

		if err := c.db.DeleteTenant(tenant, r.ParamIs("recurse", "t")); err != nil {
			r.Fail(route.Oops(err, "Unable to delete tenant '%s' (%s)", r.Args[1], tenant.Name))
			return
		}

		r.Success("Successfully deleted tenant '%s' (%s)", r.Args[1], tenant.Name)
	})
	// }}}

	r.Dispatch("GET /v2/tenants/:uuid/agents", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		agents, err := c.db.GetAllAgents(&db.AgentFilter{
			SkipHidden: true,
		})
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve agent information"))
			return
		}

		r.OK(agents)
	})
	// }}}
	r.Dispatch("GET /v2/tenants/:uuid/agents/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		agent, err := c.db.GetAgent(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve agent information"))
			return
		}
		if agent == nil || agent.Hidden {
			r.Fail(route.NotFound(nil, "No such agent"))
			return
		}

		raw, err := agent.Metadata()
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve agent information"))
			return
		}

		resp := struct {
			Agent    db.Agent               `json:"agent"`
			Metadata map[string]interface{} `json:"metadata"`
		}{
			Agent:    *agent,
			Metadata: raw,
		}

		r.OK(resp)
	})
	// }}}

	r.Dispatch("GET /v2/tenants/:uuid/targets", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		targets, err := c.db.GetAllTargets(
			&db.TargetFilter{
				ForTenant:  r.Args[1],
				SkipUsed:   r.ParamIs("unused", "t"),
				SkipUnused: r.ParamIs("unused", "f"),

				UUID:       r.Param("uuid", ""),
				SearchName: r.Param("name", ""),

				ForPlugin:  r.Param("plugin", ""),
				ExactMatch: r.ParamIs("exact", "t"),
			},
		)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve targets information"))
			return
		}

		r.OK(targets)
	})
	// }}}
	r.Dispatch("POST /v2/tenants/:uuid/targets", func(r *route.Request) { // {{{
		if c.IsNotTenantEngineer(r, r.Args[1]) {
			return
		}

		tenant, err := c.db.GetTenant(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve tenant information"))
			return
		}
		if tenant == nil {
			r.Fail(route.NotFound(nil, "No such tenant"))
			return
		}

		var in struct {
			Name        string `json:"name"`
			Summary     string `json:"summary"`
			Compression string `json:"compression"`
			Plugin      string `json:"plugin"`
			Agent       string `json:"agent"`

			Config   map[string]interface{} `json:"config"`
			endpoint string
		}

		if !r.Payload(&in) {
			return
		}
		if in.Config != nil {
			b, err := json.Marshal(in.Config)
			if err != nil {
				r.Fail(route.Oops(err, "Unable to create target"))
				return
			}
			in.endpoint = string(b)
		} else {
			in.endpoint = "{}"
		}
		if r.Missing("name", in.Name, "plugin", in.Plugin, "agent", in.Agent) {
			return
		}

		if in.Compression == "" {
			in.Compression = DefaultCompressionType
		}

		if !ValidCompressionType(in.Compression) {
			r.Fail(route.Bad(err, "Invalid compression type '%s'", in.Compression))
			return
		}

		if r.ParamIs("test", "t") {
			r.Success("validation suceeded (request made in ?test=t mode)")
			return
		}

		if !ValidCompressionType(in.Compression) {
			r.Fail(route.Bad(err, "Invalid compression type '%s'", in.Compression))
			return
		}

		target, err := c.db.CreateTarget(&db.Target{
			TenantUUID:  r.Args[1],
			Name:        in.Name,
			Summary:     in.Summary,
			Plugin:      in.Plugin,
			Config:      in.Config,
			Agent:       in.Agent,
			Compression: in.Compression,
			Healthy:     true,
		})
		if target == nil || err != nil {
			r.Fail(route.Oops(err, "Unable to create new data target"))
			return
		}

		r.OK(target)
	})
	// }}}
	r.Dispatch("GET /v2/tenants/:uuid/targets/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		target, err := c.db.GetTarget(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve target information"))
			return
		}

		if target == nil || target.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "No such target"))
			return
		}

		r.OK(target)
	})
	// }}}
	r.Dispatch("PUT /v2/tenants/:uuid/targets/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantEngineer(r, r.Args[1]) {
			return
		}

		target, err := c.db.GetTarget(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve target information"))
			return
		}

		if target == nil || target.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "No such target"))
			return
		}

		var in struct {
			Name        string `json:"name"`
			Summary     string `json:"summary"`
			Compression string `json:"compression"`
			Plugin      string `json:"plugin"`
			Endpoint    string `json:"endpoint"`
			Agent       string `json:"agent"`

			Config map[string]interface{} `json:"config"`
		}
		if !r.Payload(&in) {
			return
		}
		if in.Endpoint == "" && in.Config != nil {
			b, err := json.Marshal(in.Config)
			if err != nil {
				r.Fail(route.Oops(err, "Unable to create target"))
			}
			in.Endpoint = string(b)
		}

		if in.Name != "" {
			target.Name = in.Name
		}
		if in.Summary != "" {
			target.Summary = in.Summary
		}
		if in.Plugin != "" {
			target.Plugin = in.Plugin
		}
		if in.Config != nil {
			target.Config = in.Config
		}
		if in.Agent != "" {
			target.Agent = in.Agent
		}
		if in.Compression != "" {
			if !ValidCompressionType(in.Compression) {
				r.Fail(route.Bad(err, "Invalid compression type '%s'", in.Compression))
				return
			}
			target.Compression = in.Compression
		}

		if err := c.db.UpdateTarget(target); err != nil {
			r.Fail(route.Oops(err, "Unable to update target"))
			return
		}

		r.Success("Updated target successfully")
	})
	// }}}
	r.Dispatch("DELETE /v2/tenants/:uuid/targets/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantEngineer(r, r.Args[1]) {
			return
		}

		target, err := c.db.GetTarget(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve target information"))
			return
		}

		if target == nil || target.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "No such target"))
			return
		}

		deleted, err := c.db.DeleteTarget(target.UUID)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to delete target"))
			return
		}
		if !deleted {
			r.Fail(route.Forbidden(nil, "The target cannot be deleted at this time"))
			return
		}

		r.Success("Target deleted successfully")
	})
	// }}}

	r.Dispatch("GET /v2/tenants/:uuid/stores", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		stores, err := c.db.GetAllStores(
			&db.StoreFilter{
				SkipUsed:   r.ParamIs("unused", "t"),
				SkipUnused: r.ParamIs("unused", "f"),

				UUID:       r.Param("uuid", ""),
				SearchName: r.Param("name", ""),

				ForPlugin:  r.Param("plugin", ""),
				ExactMatch: r.ParamIs("exact", "t"),
				ForTenant:  r.Args[1],
			},
		)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve storage systems information"))
			return
		}

		r.OK(stores)
	})
	// }}}
	r.Dispatch("GET /v2/tenants/:uuid/stores/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		store, err := c.db.GetStore(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve storage system information"))
			return
		}

		if store == nil || store.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "No such storage system"))
			return
		}

		r.OK(store)
	})
	// }}}""
	r.Dispatch("GET /v2/tenants/:uuid/stores/:uuid/config", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		store, err := c.db.GetStore(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve storage system information"))
			return
		}

		if store == nil || store.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "No such storage system"))
			return
		}

		config, err := store.Configuration(c.db, c.CanSeeCredentials(r, store.TenantUUID))
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve storage system information"))
			return
		}

		r.OK(config)
	})
	// }}}""
	r.Dispatch("POST /v2/tenants/:uuid/stores", func(r *route.Request) { // {{{
		if c.IsNotTenantEngineer(r, r.Args[1]) {
			return
		}

		var in struct {
			Name      string `json:"name"`
			Summary   string `json:"summary"`
			Agent     string `json:"agent"`
			Plugin    string `json:"plugin"`
			Threshold int64  `json:"threshold"`

			Config map[string]interface{} `json:"config"`
		}

		if !r.Payload(&in) {
			return
		}

		if r.Missing("name", in.Name, "agent", in.Agent, "plugin", in.Plugin, "threshold", fmt.Sprint(in.Threshold)) {
			return
		}

		tenant, err := c.db.GetTenant(r.Args[1])
		if tenant == nil || err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve storage system information"))
			return
		}

		if r.ParamIs("test", "t") {
			r.Success("validation suceeded (request made in ?test=t mode)")
			return
		}

		store, err := c.db.CreateStore(&db.Store{
			TenantUUID: tenant.UUID,
			Name:       in.Name,
			Summary:    in.Summary,
			Agent:      in.Agent,
			Plugin:     in.Plugin,
			Config:     in.Config,
			Threshold:  in.Threshold,
			Healthy:    true, /* let's be optimistic */
		})
		if store == nil || err != nil {
			r.Fail(route.Oops(err, "Unable to create new storage system"))
			return
		}

		if _, err := c.db.CreateTestStoreTask("system", store); err != nil {
			log.Errorf("failed to schedule storage test task (non-critical) for %s (%s): %s",
				store.Name, store.UUID, err)
		}

		r.OK(store)
	})
	// }}}
	r.Dispatch("PUT /v2/tenants/:uuid/stores/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantEngineer(r, r.Args[1]) {
			return
		}

		var in struct {
			Name      string `json:"name"`
			Summary   string `json:"summary"`
			Agent     string `json:"agent"`
			Plugin    string `json:"plugin"`
			Threshold int64  `json:"threshold"`

			Config map[string]interface{} `json:"config"`
		}
		if !r.Payload(&in) {
			r.Fail(route.Bad(nil, "Unable to update storage system"))
			return
		}

		store, err := c.db.GetStore(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve storage system information"))
			return
		}
		if store == nil || store.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(err, "No such storage system"))
			return
		}

		if in.Name != "" {
			store.Name = in.Name
		}
		if in.Summary != "" {
			store.Summary = in.Summary
		}
		if in.Agent != "" {
			store.Agent = in.Agent
		}
		if in.Plugin != "" {
			store.Plugin = in.Plugin
		}
		if in.Threshold != 0 {
			store.Threshold = in.Threshold
		}

		if in.Config != nil {
			store.Config = in.Config
		}

		if err := c.db.UpdateStore(store); err != nil {
			r.Fail(route.Oops(err, "Unable to update storage system"))
			return
		}

		store, err = c.db.GetStore(store.UUID)
		if store == nil || err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve storage system information"))
			return
		}

		if _, err := c.db.CreateTestStoreTask("system", store); err != nil {
			log.Errorf("failed to schedule storage test task (non-critical) for %s (%s): %s",
				store.Name, store.UUID, err)
		}

		r.OK(store)
	})
	r.Dispatch("POST /v2/tenants/:uuid/stores/:uuid/test", func(r *route.Request) { // {{{
		// if c.IsNotTenantOperator(r, r.Args[1]) {
		// 	return
		// }

		store, err := c.db.GetStore(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve storage system information"))
			return
		}

		if store == nil || store.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "No such store"))
			return
		}

		// if _, err = c.db.PauseJob(job.UUID); err != nil {
		// 	r.Fail(route.Oops(err, "Unable to pause job"))
		// 	return
		// }
		if _, err := c.db.CreateTestStoreTask("system", store); err != nil {
			log.Errorf("failed to schedule storage test task (non-critical) for %s (%s): %s",
						store.Name, store.UUID, err)
		}

		r.Success("Testing store")
	})
	// }}}
	r.Dispatch("DELETE /v2/tenants/:uuid/stores/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantEngineer(r, r.Args[1]) {
			return
		}

		store, err := c.db.GetStore(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve storage system information"))
			return
		}
		if store == nil || store.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(err, "No such storage system"))
			return
		}

		deleted, err := c.db.DeleteStore(store.UUID)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to delete storage system"))
			return
		}
		if !deleted {
			r.Fail(route.Bad(nil, "The storage system cannot be deleted at this time"))
			return
		}

		r.Success("Storage system deleted successfully")
	})
	// }}}

	r.Dispatch("GET /v2/tenants/:uuid/jobs", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		jobs, err := c.db.GetAllJobs(
			&db.JobFilter{
				ForTenant:    r.Args[1],
				SkipPaused:   r.ParamIs("paused", "f"),
				SkipUnpaused: r.ParamIs("paused", "t"),

				UUID:       r.Param("uuid", ""),
				SearchName: r.Param("name", ""),

				ForTarget:  r.Param("target", ""),
				ForStore:   r.Param("store", ""),
				ExactMatch: r.ParamIs("exact", "t"),
			},
		)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve tenant job information."))
			return
		}

		r.OK(jobs)
	})
	// }}}
	r.Dispatch("POST /v2/tenants/:uuid/jobs", func(r *route.Request) { // {{{
		if c.IsNotTenantEngineer(r, r.Args[1]) {
			return
		}

		var in struct {
			Name     string `json:"name"`
			Summary  string `json:"summary"`
			Schedule string `json:"schedule"`
			Paused   bool   `json:"paused"`
			Store    string `json:"store"`
			Target   string `json:"target"`
			Retain   string `json:"retain"`
			FixedKey bool   `json:"fixed_key"`
			Retries  int    `json:"retries"`
		}
		if !r.Payload(&in) {
			return
		}

		if r.Missing("name", in.Name, "store", in.Store, "target", in.Target, "schedule", in.Schedule, "retain", in.Retain) {
			return
		}

		sched, err := timespec.Parse(in.Schedule)
		if err != nil {
			r.Fail(route.Oops(err, "Invalid or malformed SHIELD Job Schedule '%s'", in.Schedule))
			return
		}

		keepdays := util.ParseRetain(in.Retain)
		if keepdays < 0 {
			r.Fail(route.Oops(nil, "Invalid or malformed SHIELD Job Archive Retention Period '%s'", in.Retain))
			return
		}
		if keepdays < c.Config.Limit.Retention.Min {
			r.Fail(route.Oops(nil, "SHIELD Job Archive Retention Period '%s' is too short, archives must be kept for a minimum of %d days", in.Retain, c.Config.Limit.Retention.Min))
			return
		}
		if keepdays > c.Config.Limit.Retention.Max {
			r.Fail(route.Oops(nil, "SHIELD Job Archive Retention Period '%s' is too long, archives may be kept for a maximum of %d days", in.Retain, c.Config.Limit.Retention.Max))
			return
		}
		keepn := sched.KeepN(keepdays)

		//retries := util.ParseRetain(in.Retries)

		job, err := c.db.CreateJob(&db.Job{
			TenantUUID: r.Args[1],
			Name:       in.Name,
			Summary:    in.Summary,
			Schedule:   in.Schedule,
			KeepDays:   keepdays,
			KeepN:      keepn,
			Paused:     in.Paused,
			StoreUUID:  in.Store,
			TargetUUID: in.Target,
			FixedKey:   in.FixedKey,
			Retries:    in.Retries,
		})
		if job == nil || err != nil {
			r.Fail(route.Oops(err, "Unable to create new job"))
			return
		}

		r.OK(job)
	})
	// }}}
	r.Dispatch("GET /v2/tenants/:uuid/jobs/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		job, err := c.db.GetJob(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve job information"))
			return
		}

		if job == nil || job.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "No such job"))
			return
		}

		r.OK(job)
	})
	// }}}
	r.Dispatch("PUT /v2/tenants/:uuid/jobs/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantEngineer(r, r.Args[1]) {
			return
		}

		var in struct {
			Name     string `json:"name"`
			Summary  string `json:"summary"`
			Schedule string `json:"schedule"`
			Retain   string `json:"retain"`

			StoreUUID  string `json:"store"`
			TargetUUID string `json:"target"`
			FixedKey   *bool  `json:"fixed_key"`
			Retries    int    `json:"retries"`
		}
		if !r.Payload(&in) {
			return
		}

		job, err := c.db.GetJob(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve job information"))
			return
		}
		if job == nil || job.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "No such job"))
			return
		}

		if in.Name != "" {
			job.Name = in.Name
		}
		if in.Summary != "" {
			job.Summary = in.Summary
		}
		if in.Schedule != "" {
			if _, err := timespec.Parse(in.Schedule); err != nil {
				r.Fail(route.Oops(err, "Invalid or malformed SHIELD Job Schedule '%s'", in.Schedule))
				return
			}
			job.Schedule = in.Schedule
		}
		if in.Retain != "" {
			keepdays := util.ParseRetain(in.Retain)
			if keepdays < 0 {
				r.Fail(route.Oops(nil, "Invalid or malformed SHIELD Job Archive Retention Period '%s'", in.Retain))
				return
			}
			if keepdays < c.Config.Limit.Retention.Min {
				r.Fail(route.Oops(nil, "SHIELD Job Archive Retention Period '%s' is too short, archives must be kept for a minimum of %d days", in.Retain, c.Config.Limit.Retention.Min))
				return
			}
			if keepdays > c.Config.Limit.Retention.Max {
				r.Fail(route.Oops(nil, "SHIELD Job Archive Retention Period '%s' is too long, archives may be kept for a maximum of %d days", in.Retain, c.Config.Limit.Retention.Max))
				return
			}

			job.KeepDays = keepdays
			job.KeepN = -1
			if sched, err := timespec.Parse(job.Schedule); err == nil {
				job.KeepN = sched.KeepN(job.KeepDays)
			}
		}

		job.TargetUUID = job.Target.UUID
		if in.TargetUUID != "" {
			job.TargetUUID = in.TargetUUID
		}
		job.StoreUUID = job.Store.UUID
		if in.StoreUUID != "" {
			job.StoreUUID = in.StoreUUID
		}
		if in.FixedKey != nil {
			job.FixedKey = *in.FixedKey
		}
		if in.Retries >= 0 {
			job.Retries = in.Retries
		}
		if err := c.db.UpdateJob(job); err != nil {
			r.Fail(route.Oops(err, "Unable to update job"))
			return
		}

		if in.Schedule != "" {
			if spec, err := timespec.Parse(in.Schedule); err == nil {
				if next, err := spec.Next(time.Now()); err == nil {
					c.db.RescheduleJob(job, next)
				}
			}
		}

		r.Success("Updated job successfully")
	})
	// }}}
	r.Dispatch("DELETE /v2/tenants/:uuid/jobs/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantEngineer(r, r.Args[1]) {
			return
		}

		job, err := c.db.GetJob(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve job information"))
			return
		}

		if job == nil || job.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "No such job"))
			return
		}

		deleted, err := c.db.DeleteJob(job.UUID)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to delete job"))
			return
		}
		if !deleted {
			r.Fail(route.Forbidden(nil, "The job cannot be deleted at this time"))
			return
		}

		r.Success("Job deleted successfully")
	})
	// }}}
	r.Dispatch("POST /v2/tenants/:uuid/jobs/:uuid/run", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		job, err := c.db.GetJob(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve job information"))
			return
		}

		if job == nil || job.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "No such job"))
			return
		}

		user, _ := c.AuthenticatedUser(r)
		task, err := c.db.CreateBackupTask(fmt.Sprintf("%s@%s", user.Account, user.Backend), job)
		if task == nil || err != nil {
			r.Fail(route.Oops(err, "Unable to schedule ad hoc backup job run"))
			return
		}

		var out struct {
			OK       string `json:"ok"`
			TaskUUID string `json:"task_uuid"`
		}

		out.OK = "Scheduled ad hoc backup job run"
		out.TaskUUID = task.UUID
		r.OK(out)
	})
	// }}}
	r.Dispatch("POST /v2/tenants/:uuid/jobs/:uuid/pause", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		job, err := c.db.GetJob(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve job information"))
			return
		}

		if job == nil || job.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "No such job"))
			return
		}

		if _, err = c.db.PauseJob(job.UUID); err != nil {
			r.Fail(route.Oops(err, "Unable to pause job"))
			return
		}
		r.Success("Paused job successfully")
	})
	// }}}
	r.Dispatch("POST /v2/tenants/:uuid/jobs/:uuid/unpause", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		job, err := c.db.GetJob(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve job information"))
			return
		}

		if job == nil || job.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "No such job"))
			return
		}

		if _, err = c.db.UnpauseJob(job.UUID); err != nil {
			r.Fail(route.Oops(err, "Unable to unpause job"))
			return
		}
		r.Success("Unpaused job successfully")
	})
	// }}}

	r.Dispatch("GET /v2/tenants/:uuid/tasks", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		limit, err := strconv.Atoi(r.Param("limit", "30"))
		if err != nil || limit < 0 || limit > 30 {
			r.Fail(route.Bad(err, "Invalid limit parameter given"))
			return
		}

		// check to see if we're offseting task requests
		paginationDate, err := strconv.ParseInt(r.Param("before", "0"), 10, 64)
		if err != nil || paginationDate < 0 {
			r.Fail(route.Bad(err, "Invalid before parameter given"))
			return
		}

		tasks, err := c.db.GetAllTasks(
			&db.TaskFilter{
				UUID:          r.Param("uuid", ""),
				ExactMatch:    r.ParamIs("exact", "t"),
				SkipActive:    r.ParamIs("active", "f"),
				SkipInactive:  r.ParamIs("active", "t"),
				ForStatus:     r.Param("status", ""),
				ForTarget:     r.Param("target", ""),
				ForStore:      r.Param("store", ""),
				ForOp:         r.Param("type", ""),
				ForTenant:     r.Args[1],
				Limit:         limit,
				Before:        paginationDate,
				StartedAfter:  r.ParamDuration("started_after"),
				StoppedAfter:  r.ParamDuration("stopped_after"),
				StartedBefore: r.ParamDuration("started_before"),
				StoppedBefore: r.ParamDuration("stopped_before"),
			},
		)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve task information"))
			return
		}

		if !c.CanSeeCredentials(r, r.Args[1]) {
			c.db.RedactAllTaskLogs(tasks)
		}
		r.OK(tasks)
	})
	// }}}
	r.Dispatch("GET /v2/tenants/:uuid/tasks/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		task, err := c.db.GetTask(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve task information"))
			return
		}
		if task == nil || task.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(err, "No such task"))
			return
		}
		if !c.CanSeeCredentials(r, r.Args[1]) {
			c.db.RedactTaskLog(task)
		}
		r.OK(task)
	})
	// }}}
	r.Dispatch("DELETE /v2/tenants/:uuid/tasks/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		task, err := c.db.GetTask(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve task information"))
			return
		}
		if task == nil || task.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(err, "No such task"))
			return
		}

		if err := c.db.CancelTask(task.UUID, time.Now()); err != nil {
			r.Fail(route.Oops(err, "Unable to cancel task"))
			return
		}

		r.Success("Canceled task successfully")
	})
	// }}}

	r.Dispatch("GET /v2/tenants/:uuid/archives", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		limit, err := strconv.Atoi(r.Param("limit", "0"))
		if err != nil || limit < 0 {
			r.Fail(route.Bad(err, "Invalid limit parameter given"))
			return
		}

		status := []string{}
		if s := r.Param("status", ""); s != "" {
			status = append(status, s)
		}

		archives, err := c.db.GetAllArchives(
			&db.ArchiveFilter{
				UUID:       r.Param("uuid", ""),
				ExactMatch: r.ParamIs("exact", "t"),
				ForTenant:  r.Args[1],
				ForTarget:  r.Param("target", ""),
				ForStore:   r.Param("store", ""),
				Before:     r.ParamDate("before"),
				After:      r.ParamDate("after"),
				WithStatus: status,
				Limit:      limit,
			},
		)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve backup archives information"))
			return
		}

		r.OK(archives)
	})
	// }}}
	r.Dispatch("GET /v2/tenants/:uuid/archives/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		archive, err := c.db.GetArchive(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve backup archive information"))
			return
		}

		if archive == nil || archive.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "Archive Not Found"))
			return
		}

		r.OK(archive)
	})
	// }}}
	r.Dispatch("PUT /v2/tenants/:uuid/archives/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		var in struct {
			Notes string `json:"notes"`
		}
		if !r.Payload(&in) {
			return
		}

		archive, err := c.db.GetArchive(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve backup archive information"))
			return
		}

		if archive == nil || archive.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "No such backup archive"))
			return
		}

		if r.Missing("notes", in.Notes) {
			return
		}

		archive.Notes = in.Notes
		if err := c.db.UpdateArchive(archive); err != nil {
			r.Fail(route.Oops(err, "Unable to update backup archive"))
			return
		}

		r.OK(archive)
	})
	// }}}
	r.Dispatch("DELETE /v2/tenants/:uuid/archives/:uuid", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		archive, err := c.db.GetArchive(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve backup archive information"))
			return
		}

		if archive == nil || archive.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "No such backup archive"))
			return
		}

		if archive.Status != "valid" {
			r.Fail(route.Bad(err, "The backup archive could not be deleted at this time. Archive is already %s", archive.Status))
		}

		err = c.db.ManuallyPurgeArchive(archive.UUID)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to delete backup archive"))
			return
		}
		err = c.vault.Delete(fmt.Sprintf("secret/archives/%s", archive.UUID))
		if err != nil {
			log.Errorf("failed to delete encryption parameters for archive %s: %s", archive.UUID, err)
		}

		r.Success("Archive deleted successfully")
	})
	// }}}
	r.Dispatch("POST /v2/tenants/:uuid/archives/:uuid/restore", func(r *route.Request) { // {{{
		if c.IsNotTenantOperator(r, r.Args[1]) {
			return
		}

		var in struct {
			Target string `json:"target"`
		}
		if !r.Payload(&in) {
			return
		}

		archive, err := c.db.GetArchive(r.Args[2])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve backup archive information"))
			return
		}
		if archive == nil || archive.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "No such backup archive"))
			return
		}

		if in.Target == "" {
			in.Target = archive.TargetUUID
		}

		target, err := c.db.GetTarget(in.Target)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve backup archive information"))
			return
		}

		if target == nil || archive.TenantUUID != r.Args[1] {
			r.Fail(route.NotFound(nil, "No such backup archive"))
			return
		}

		user, _ := c.AuthenticatedUser(r)
		task, err := c.db.CreateRestoreTask(fmt.Sprintf("%s@%s", user.Account, user.Backend), archive, target)
		if task == nil || err != nil {
			r.Fail(route.Oops(err, "Unable to schedule a restore task"))
			return
		}
		if !c.CanSeeCredentials(r, r.Args[1]) {
			c.db.RedactTaskLog(task)
		}
		r.OK(task)
	})
	// }}}

	r.Dispatch("POST /v2/auth/login", func(r *route.Request) { // {{{
		var in struct {
			Username string
			Password string
		}
		if !r.Payload(&in) {
			return
		}

		if r.Missing("username", in.Username, "password", in.Password) {
			return
		}

		user, err := c.db.GetUser(in.Username, "local")
		if err != nil {
			r.Fail(route.Oops(err, "Unable to log you in"))
			return
		}

		if user == nil || !user.Authenticate(in.Password) {
			r.Fail(route.Errorf(401, nil, "Incorrect username or password"))
			return
		}

		session, err := c.db.CreateSession(&db.Session{
			UserUUID:  user.UUID,
			IP:        r.RemoteIP(),
			UserAgent: r.UserAgent(),
		})
		if err != nil {
			r.Fail(route.Oops(err, "Unable to log you in"))
			return
		}
		if session == nil {
			r.Fail(route.Oops(fmt.Errorf("no session created"), "Unable to log you in"))
			return
		}

		id, err := c.checkAuth(user)
		if err != nil || id == nil {
			r.Fail(route.Oops(err, "Unable to log you in"))
		}

		r.SetSession(session.UUID)
		r.OK(id)
	})
	// }}}
	r.Dispatch("GET /v2/auth/logout", func(r *route.Request) { // {{{
		if err := c.db.ClearSession(r.SessionID()); err != nil {
			r.Fail(route.Oops(err, "Unable to log you out"))
			return
		}

		r.ClearSession()
		r.Success("Successfully logged out")
	})
	// }}}
	r.Dispatch("GET /v2/auth/id", func(r *route.Request) { // {{{
		user, _ := c.AuthenticatedUser(r)
		if id, _ := c.checkAuth(user); id != nil {
			r.OK(id)
			return
		}

		r.OK(struct {
			Unauthenticated bool `json:"unauthenticated"`
		}{true})
	})
	// }}}
	r.Dispatch("POST /v2/auth/passwd", func(r *route.Request) { // {{{
		if c.IsNotAuthenticated(r) {
			return
		}

		var in struct {
			OldPassword string `json:"old_password"`
			NewPassword string `json:"new_password"`
		}

		if !r.Payload(&in) {
			return
		}

		user, _ := c.AuthenticatedUser(r)
		if !user.Authenticate(in.OldPassword) {
			r.Fail(route.Forbidden(nil, "Incorrect password"))
			return
		}

		user.SetPassword(in.NewPassword)
		if err := c.db.UpdateUser(user); err != nil {
			r.Fail(route.Oops(err, "Unable to change your password"))
			return
		}

		r.Success("Password changed successfully")
	})
	// }}}
	r.Dispatch("PATCH /v2/auth/user/settings", func(r *route.Request) { // {{{
		var in struct {
			DefaultTenant string `json:"default_tenant"`
		}

		if !r.Payload(&in) {
			return
		}

		user, err := c.AuthenticatedUser(r)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to save settings"))
			return
		}

		if in.DefaultTenant != "" {
			user.DefaultTenant = in.DefaultTenant
		}
		if err := c.db.UpdateUserSettings(user); err != nil {
			r.Fail(route.Oops(err, "Unable to save settings"))
			return
		}

		r.Success("Settings saved")
	})
	// }}}

	r.Dispatch("GET /v2/global/stores", func(r *route.Request) { // {{{
		if c.IsNotAuthenticated(r) {
			return
		}

		stores, err := c.db.GetAllStores(
			&db.StoreFilter{
				SkipUsed:   r.ParamIs("unused", "t"),
				SkipUnused: r.ParamIs("unused", "f"),

				UUID:       r.Param("uuid", ""),
				SearchName: r.Param("name", ""),

				ForPlugin:  r.Param("plugin", ""),
				ExactMatch: r.ParamIs("exact", "t"),
				ForTenant:  db.GlobalTenantUUID,
			},
		)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve storage systems information"))
			return
		}

		r.OK(stores)
	})
	// }}}
	r.Dispatch("GET /v2/global/stores/:uuid", func(r *route.Request) { // {{{
		if c.IsNotAuthenticated(r) {
			return
		}

		store, err := c.db.GetStore(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve storage system information"))
			return
		}

		if store == nil || store.TenantUUID != db.GlobalTenantUUID {
			r.Fail(route.NotFound(nil, "No such storage system"))
			return
		}

		r.OK(store)
	})
	// }}}""
	r.Dispatch("GET /v2/global/stores/:uuid/config", func(r *route.Request) { // {{{
		if c.IsNotSystemEngineer(r) {
			return
		}

		store, err := c.db.GetStore(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve storage system information"))
			return
		}

		if store == nil || store.TenantUUID != db.GlobalTenantUUID {
			r.Fail(route.NotFound(nil, "No such storage system"))
			return
		}

		config, err := store.Configuration(c.db, true)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve storage system information"))
			return
		}
		r.OK(config)
	})
	// }}}""
	r.Dispatch("POST /v2/global/stores", func(r *route.Request) { // {{{
		if c.IsNotSystemEngineer(r) {
			return
		}

		var in struct {
			Name      string `json:"name"`
			Summary   string `json:"summary"`
			Agent     string `json:"agent"`
			Plugin    string `json:"plugin"`
			Threshold int64  `json:"threshold"`

			Config map[string]interface{} `json:"config"`
		}

		if !r.Payload(&in) {
			return
		}

		if r.Missing("name", in.Name, "agent", in.Agent, "plugin", in.Plugin, "threshold", fmt.Sprint(in.Threshold)) {
			return
		}

		store, err := c.db.CreateStore(&db.Store{
			TenantUUID: db.GlobalTenantUUID,
			Name:       in.Name,
			Summary:    in.Summary,
			Agent:      in.Agent,
			Plugin:     in.Plugin,
			Config:     in.Config,
			Threshold:  in.Threshold,
			Healthy:    true, /* let's be optimistic */
		})
		if store == nil || err != nil {
			r.Fail(route.Oops(err, "Unable to create new storage system"))
			return
		}

		r.OK(store)
	})
	// }}}
	r.Dispatch("PUT /v2/global/stores/:uuid", func(r *route.Request) { // {{{
		if c.IsNotSystemEngineer(r) {
			return
		}

		var in struct {
			Name      string `json:"name"`
			Summary   string `json:"summary"`
			Agent     string `json:"agent"`
			Plugin    string `json:"plugin"`
			Threshold int64  `json:"threshold"`

			Config map[string]interface{} `json:"config"`
		}
		if !r.Payload(&in) {
			r.Fail(route.Bad(nil, "Unable to update storage system"))
			return
		}

		store, err := c.db.GetStore(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve storage system information"))
			return
		}
		if store == nil || store.TenantUUID != db.GlobalTenantUUID {
			r.Fail(route.NotFound(err, "No such storage system"))
			return
		}

		if in.Name != "" {
			store.Name = in.Name
		}
		if in.Summary != "" {
			store.Summary = in.Summary
		}
		if in.Agent != "" {
			store.Agent = in.Agent
		}
		if in.Plugin != "" {
			store.Plugin = in.Plugin
		}
		if in.Threshold != 0 {
			store.Threshold = in.Threshold
		}
		if in.Config != nil {
			store.Config = in.Config
		}

		if err := c.db.UpdateStore(store); err != nil {
			r.Fail(route.Oops(err, "Unable to update storage system"))
			return
		}

		store, err = c.db.GetStore(store.UUID)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve storage system information"))
			return
		}

		r.OK(store)
	})
	// }}}
	r.Dispatch("DELETE /v2/global/stores/:uuid", func(r *route.Request) { // {{{
		if c.IsNotSystemEngineer(r) {
			return
		}

		store, err := c.db.GetStore(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve storage system information"))
			return
		}
		if store == nil || store.TenantUUID != db.GlobalTenantUUID {
			r.Fail(route.NotFound(err, "No such storage system"))
			return
		}

		deleted, err := c.db.DeleteStore(store.UUID)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to delete storage system"))
			return
		}
		if !deleted {
			r.Fail(route.Bad(nil, "The storage system cannot be deleted at this time"))
			return
		}

		r.Success("Storage system deleted successfully")
	})
	// }}}

	r.Dispatch("GET /v2/fixups", func(r *route.Request) { // {{{
		if c.IsNotSystemEngineer(r) {
			return
		}

		fixups, err := c.db.GetAllFixups(nil)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve data fixups information"))
			return
		}

		r.OK(fixups)
	})
	// }}}
	r.Dispatch("GET /v2/fixups/:id", func(r *route.Request) { // {{{
		if c.IsNotSystemEngineer(r) {
			return
		}
		fixup, err := c.db.GetFixup(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve data fixup"))
			return
		}

		r.OK(fixup)
	})
	// }}}
	r.Dispatch("POST /v2/fixups/:id/apply", func(r *route.Request) { // {{{
		if c.IsNotSystemEngineer(r) {
			return
		}

		fixup, err := c.db.GetFixup(r.Args[1])
		if err != nil {
			r.Fail(route.Oops(err, "Unable to retrieve data fixups information"))
			return
		}

		err = fixup.ReApply(c.db)
		if err != nil {
			r.Fail(route.Oops(err, "Unable to apply data fixup successfully"))
			return
		}

		r.Success("applied fixup")
	})
	// }}}

	r.Dispatch("POST /v2/bootstrap/restore", func(r *route.Request) { // {{{
		if c.IsNotSystemAdmin(r) {
			return
		}

		log.Infof("BOOTSTRAP: streaming uploaded archive file...")
		in, _, err := r.Req.FormFile("archive")
		if err != nil {
			r.Fail(route.Oops(err, "Unable to stream uploaded backup archive"))
			return
		}

		log.Infof("BOOTSTRAP: deriving encryption parameters from provided fixed key...")
		/* derive encryption parameters from fixed key */
		key := regexp.MustCompile(`\s`).ReplaceAll([]byte(r.Req.FormValue("key")), nil)
		if !regexp.MustCompile(`^[A-Fa-f0-9]*$`).Match(key) || len(key) != 1024 {
			r.Fail(route.Oops(nil, "Invalid SHIELD Fixed Key (must be 1024 hex digits)"))
			return
		}
		enc, err := vault.DeriveFixedParameters(key)
		if err != nil {
			r.Fail(route.Oops(err, "Invalid SHIELD Fixed Key (unable to use it to derive encryption parameters)"))
			return
		}

		log.Infof("BOOTSTRAP: Initializing SHIELD core for restore...")
		status, err := c.vault.Status()
		if err != nil {
			r.Fail(route.Oops(err, "Unable to initialize the SHIELD Core"))
			return
		}
		if status != vault.Blank {
			r.Fail(route.Bad(nil, "this SHIELD Core has already been initialized"))
			return
		}
		fixedKey, err := c.vault.Initialize(c.CryptFile(), "master")
		if err != nil {
			r.Fail(route.Oops(err, "Unable to initialize the SHIELD Core"))
			return
		}

		log.Infof("BOOTSTRAP: generating auth token for SHIELD restore...")
		token := r.Req.FormValue("token")

		/* execute the shield-recover command */
		log.Infof("BOOTSTRAP: executing shield-recover process...")
		cmd := exec.Command("shield-recover")
		cmd.Stdin = in
		cmd.Env = append(cmd.Env, fmt.Sprintf("PATH=%s:%s", strings.Join(c.Config.PluginPaths, ":"), os.Getenv("PATH")))
		cmd.Env = append(cmd.Env, fmt.Sprintf("SHIELD_DATA_DIR=%s", c.Config.DataDir))
		cmd.Env = append(cmd.Env, fmt.Sprintf("SHIELD_RESTARTER=%s", c.Config.Bootstrapper))
		cmd.Env = append(cmd.Env, fmt.Sprintf("SHIELD_ENCRYPT_TYPE=%s", enc.Type))
		cmd.Env = append(cmd.Env, fmt.Sprintf("SHIELD_ENCRYPT_KEY=%s", enc.Key))
		cmd.Env = append(cmd.Env, fmt.Sprintf("SHIELD_ENCRYPT_IV=%s", enc.IV))
		cmd.Env = append(cmd.Env, fmt.Sprintf("SHIELD_AUTH_TOKEN=%s", token))
		cmd.Env = append(cmd.Env, fmt.Sprintf("SHIELD_TASK_UUID=%s", "BOOTSTRAP-RESTORE"))

		if err := cmd.Run(); err != nil {
			log.Errorf("BOOTSTRAP: command exited abnormally (%s)", err)
			r.Fail(route.Oops(err, "SHIELD Restore Failed: You may be in a broken state."))
			return
		}

		log.Errorf("BOOTSTRAP: RESTORED SUCCESSFULLY; removing bootstrap.log")
		os.Remove(c.DataFile("bootstrap.old"))
		os.Rename(c.DataFile("bootstrap.log"), c.DataFile("bootstrap.old"))

		r.OK(
			struct {
				Response string `json:"response"`
				FixedKey string `json:"fixed_key"`
			}{
				"Successfully initialized the SHIELD Core",
				fixedKey,
			})
		return
	}) // }}}

	r.Dispatch("GET /v2/bootstrap/log", func(r *route.Request) { // {{{
		if c.IsNotSystemAdmin(r) {
			return
		}
		b, err := ioutil.ReadFile(c.DataFile("bootstrap.log"))
		if err != nil {
			log.Errorf("unable to read bootstrap.log: %s", err)
		}

		r.OK(struct {
			Log string `json:"log"`
		}{Log: string(b)})
	}) // }}}

	r.Dispatch("GET /v2/export", func(r *route.Request) { // {{{
		if c.IsNotSystemAdmin(r) {
			return
		}

		st, err := c.vault.StatusString()
		if err != nil {
			r.Fail(route.Oops(err, "Failed to export SHIELD data"))
			return
		}
		if st != "unlocked" {
			r.Fail(route.Oops(fmt.Errorf("vault is locked"), "Failed to export SHIELD data"))
			return
		}

		if out := r.JSONEncoder(); out != nil {
			c.db.Export(out, c.vault, r.Param("task", ""))
		} else {
			r.Fail(route.Oops(nil, "Failed to export SHIELD data"))
		}
	}) // }}}
	r.Dispatch("POST /v2/import", func(r *route.Request) { // {{{
		if c.IsNotSystemAdmin(r) {
			return
		}

		st, err := c.vault.StatusString()
		if err != nil {
			r.Fail(route.Oops(err, "Failed to import SHIELD data"))
			return
		}
		if st != "unlocked" {
			r.Fail(route.Oops(fmt.Errorf("vault is locked"), "Failed to import SHIELD data"))
			return
		}

		if in := r.JSONDecoder(); in != nil {

			err = c.db.Import(in, c.vault, r.Param("key", ""), r.Param("task", ""))
			if err != nil {
				route.Oops(err, "Empty key and/or task")
				// r.Fail(route.Oops(err, "Failed to import SHIELD data"))
				// return
			}
			r.Success("imported successfully: %s    %s", r.Param("key", ""), r.Param("task", ""))
		} else {
			r.Fail(route.Oops(nil, "Failed to import SHIELD data"))
		}
	}) // }}}

	return r
}

func (c *Core) v2copyTarget(dst *v2System, target *db.Target) error {
	dst.UUID = target.UUID
	dst.Name = target.Name
	dst.Notes = target.Summary
	dst.OK = true
	dst.Compression = target.Compression

	jobs, err := c.db.GetAllJobs(&db.JobFilter{ForTarget: target.UUID})
	if err != nil {
		return err
	}

	dst.Jobs = make([]v2SystemJob, len(jobs))
	for j, job := range jobs {
		dst.Jobs[j].UUID = job.UUID
		dst.Jobs[j].Schedule = job.Schedule
		dst.Jobs[j].From = job.Target.Plugin
		dst.Jobs[j].To = job.Store.Plugin
		dst.Jobs[j].OK = job.Healthy
		dst.Jobs[j].Store.UUID = job.Store.UUID
		dst.Jobs[j].Store.Name = job.Store.Name
		dst.Jobs[j].Store.Summary = job.Store.Summary
		dst.Jobs[j].Store.Healthy = job.Store.Healthy

		if !job.Healthy {
			dst.OK = false
		}

		tspec, err := timespec.Parse(job.Schedule)
		if err != nil {
			return err
		}
		switch tspec.Interval {
		case timespec.Minutely:
			dst.Jobs[j].Keep.N = dst.Jobs[j].Keep.Days * 1440 / int(tspec.Cardinality)
		case timespec.Hourly:
			if tspec.Cardinality == 0 {
				dst.Jobs[j].Keep.N = dst.Jobs[j].Keep.Days * 24
			} else {
				dst.Jobs[j].Keep.N = dst.Jobs[j].Keep.Days * 24 / int(tspec.Cardinality)
			}
		case timespec.Daily:
			dst.Jobs[j].Keep.N = dst.Jobs[j].Keep.Days
		case timespec.Weekly:
			dst.Jobs[j].Keep.N = dst.Jobs[j].Keep.Days / 7
		case timespec.Monthly:
			dst.Jobs[j].Keep.N = dst.Jobs[j].Keep.Days / 30
		}
	}
	return nil
}
