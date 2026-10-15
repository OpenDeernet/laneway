package ui

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// n on the board: one form with the issue's type, summary and description,
// created in the board's project, in the sprint when a sprint is showing.
// (A subtask, an epic child and a roadmap epic still pick a type, then
// type a summary.)

// jiraCreatedMsg reports a create.
type jiraCreatedMsg struct {
	key  string
	err  error
	form *jiraFormState // the fields a failed create lacks, to fill in
}

// The create form's own rows, written to the issue itself rather than as
// fields.
const (
	createTypeField    = "issuetype"
	createSummaryField = "summary"
	createDescField    = "description"
)

// jiraCreateTypesMsg is a project's issue types, to open the create form:
// for a new issue, or a subtask or epic child when parent is set.
type jiraCreateTypesMsg struct {
	project, parent string
	types           []jira.Option
	err             error
}

// openJiraCreate loads the project's issue types for the create form.
func (m *Model) openJiraCreate() tea.Cmd {
	project := m.jiraTab.project
	if project == "" {
		return nil
	}
	m.status = i18n.Tf("new issue in %s…", project)
	c, ctx := m.jiraClient, m.ctx
	return func() tea.Msg {
		go c.WarmUsers(ctx, project) // @ in the description answers at once
		types, err := c.IssueTypes(ctx, project)
		return jiraCreateTypesMsg{project: project, types: types, err: err}
	}
}

// openJiraCreateChild loads the types a subtask ("subtask") or an epic's
// child ("child") of parent can be, for the create form.
func (m *Model) openJiraCreateChild(parent, what string) tea.Cmd {
	project, c, ctx := issueProject(parent), m.jiraClient, m.ctx
	m.status = i18n.Tf("new child of %s…", parent)
	if what == "subtask" {
		m.status = i18n.Tf("new subtask of %s…", parent)
	}
	return func() tea.Msg {
		go c.WarmUsers(ctx, project) // @ in the description answers at once
		types, err := c.IssueTypes(ctx, project)
		if what == "subtask" {
			types, err = c.SubtaskTypes(ctx, project)
		}
		types = slices.DeleteFunc(types, func(t jira.Option) bool { return strings.EqualFold(t.Name, "epic") })
		return jiraCreateTypesMsg{project: project, parent: parent, types: types, err: err}
	}
}

// handleJiraCreateTypes opens the create form: the type you last made in
// the project (else Task, else the first).
func (m Model) handleJiraCreateTypes(msg jiraCreateTypesMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil || len(msg.types) == 0 {
		m.fail(i18n.Tf("new issue: %s", cmp.Or(errText(msg.err), i18n.Tf("no issue types in %s", msg.project))))
		return m, nil
	}
	if m.modalOpen() {
		return m, nil
	}
	names := make([]string, len(msg.types))
	for i, t := range msg.types {
		names[i] = t.Name
	}
	return m, m.openCreateForm(createSpec{in: jira.NewIssue{Project: msg.project, Parent: msg.parent}, types: names})
}

// jiraCloneDraftMsg is the copy of cloneOf to start the create form from.
type jiraCloneDraftMsg struct {
	cloneOf string
	in      jira.NewIssue
	err     error
}

// openJiraClone copies key into the create form.
func (m *Model) openJiraClone(key string) tea.Cmd {
	c, ctx := m.jiraClient, m.ctx
	m.status = i18n.Tf("copying %s…", key)
	return func() tea.Msg {
		in, err := c.CloneDraft(ctx, key)
		return jiraCloneDraftMsg{cloneOf: key, in: in, err: err}
	}
}

func (m Model) handleJiraCloneDraft(msg jiraCloneDraftMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.fail(i18n.Tf("clone: %s", msg.err.Error()))
		return m, nil
	}
	if m.modalOpen() {
		return m, nil
	}
	return m, m.openCreateForm(createSpec{in: msg.in, types: []string{msg.in.Type}, cloneOf: msg.cloneOf})
}

// createSpec is what the create form starts from: the issue so far (its
// project, a locked parent, a clone's copied fields), the types it may be,
// and the one to start on ("" for the last made, else Task, else the first).
type createSpec struct {
	in      jira.NewIssue
	types   []string
	typ     string
	cloneOf string
	reload  bool // a roadmap epic: the roadmap reloads once it is made
}

// openCreateForm opens the create form on s, the cursor in the summary.
func (m *Model) openCreateForm(s createSpec) tea.Cmd {
	opts := make([]jira.Option, len(s.types))
	for i, t := range s.types {
		opts[i] = jira.Option{ID: t, Name: t}
	}
	typ := opts[0].Name
	for _, want := range []string{s.typ, m.lastCreateTypeIn(s.in.Project), "Task"} {
		if i := slices.IndexFunc(opts, func(o jira.Option) bool { return strings.EqualFold(o.Name, want) }); want != "" && i >= 0 {
			typ = opts[i].Name
			break
		}
	}
	m.jiraCreateType, m.jiraCreateParent, m.jiraCreateProject = typ, s.in.Parent, s.in.Project
	m.jiraCreateClone, m.jiraCreateReload = s.cloneOf, s.reload
	summary, desc := s.in.Summary, m.opts.templates[strings.ToLower(typ)]
	cr := &jiraFormCreate{in: s.in, cloneOf: s.cloneOf, form: true}
	cr.in.Summary, cr.in.Type = "", ""
	message := ""
	if len(s.in.DescriptionADF) > 0 { // a clone's: as it is unless you edit it
		ed, err := jira.EditableDescription(s.in.DescriptionADF)
		desc, cr.descKept = ed.Markdown, ed.Kept
		if err != nil {
			desc, message = "", i18n.Tf("The description is copied from %s as it is.", s.cloneOf)
		}
	}
	f := &jiraFormState{key: m.jiraCreateTitle(), idx: 1, create: cr, message: message}
	f.fields = []jiraFormField{
		{FieldMeta: jira.FieldMeta{ID: createTypeField, Name: i18n.T("Type"), Kind: jira.KindOption, Options: opts}, required: true,
			val: jira.Value{Options: []jira.Option{{ID: typ, Name: typ}}}},
		{FieldMeta: jira.FieldMeta{ID: createSummaryField, Name: i18n.T("Summary"), Kind: jira.KindText}, required: true,
			val: jira.Value{Text: summary}, changed: summary != ""},
		{FieldMeta: jira.FieldMeta{ID: createDescField, Name: i18n.T("Description"), Kind: jira.KindDoc}, val: jira.Value{Text: desc}},
	}
	m.jiraForm = f
	m.status = ""
	return tea.Batch(m.editJiraFormField(), m.loadCreateFields())
}

// createFieldsMsg is a create screen fetched for the form's type.
type createFieldsMsg struct {
	project, typ string
	seq          int
	fields       []jira.CreateField
	err          error
}

// loadCreateFields shows the fields the form's type requires: at once when
// known, else once fetched (a later type change drops the fetch).
func (m *Model) loadCreateFields() tea.Cmd {
	f := m.jiraForm
	project, typ := f.create.in.Project, createFormType(f)
	if fields, ok := m.createFieldCache[project+"/"+typ]; ok {
		f.create.loading = false
		m.setCreateRows(fields)
		return nil
	}
	f.create.fieldsSeq++
	f.create.loading = true
	seq, c, ctx := f.create.fieldsSeq, m.jiraClient, m.ctx
	return func() tea.Msg {
		fields, err := c.CreateFields(ctx, project, typ)
		return createFieldsMsg{project: project, typ: typ, seq: seq, fields: fields, err: err}
	}
}

// handleCreateFields adds the required fields to the form still showing
// that type. A failed fetch leaves the form as it is: Jira's refusal still
// names what's missing.
func (m Model) handleCreateFields(msg createFieldsMsg) (tea.Model, tea.Cmd) {
	f := m.jiraForm
	if f == nil || f.create == nil || !f.create.form || msg.seq != f.create.fieldsSeq {
		return m, nil
	}
	f.create.loading = false
	if msg.err != nil {
		return m, nil
	}
	if m.createFieldCache == nil {
		m.createFieldCache = map[string][]jira.CreateField{}
	}
	m.createFieldCache[msg.project+"/"+msg.typ] = msg.fields
	m.setCreateRows(msg.fields)
	return m, nil
}

// setCreateRows puts the fields Jira requires of the type after the form's
// own rows, each with what was typed in it before, even under another type.
func (m *Model) setCreateRows(fields []jira.CreateField) {
	f := m.jiraForm
	if f.create.kept == nil {
		f.create.kept = map[string]jira.Value{}
	}
	own := f.fields[:0:0]
	for _, ff := range f.fields {
		switch ff.ID {
		case createTypeField, createSummaryField, createDescField:
			own = append(own, ff)
		default:
			f.create.kept[ff.ID] = ff.val
		}
	}
	f.create.screen = fields
	row := func(cf jira.CreateField) jiraFormField {
		v, typed := f.create.kept[cf.ID]
		if !typed && cf.Kind == jira.KindSprint { // the shown sprint, as the title says
			if id, name := m.createSprint(); id != 0 {
				v = jira.Value{Options: []jira.Option{{ID: strconv.Itoa(id), Name: name}}}
			}
		}
		return jiraFormField{FieldMeta: cf.FieldMeta, required: cf.Required, val: v, changed: typed && !v.Empty()}
	}
	var more []jiraFormField
	for _, cf := range fields {
		switch {
		case slices.Contains([]string{"project", createTypeField, createSummaryField, createDescField}, cf.ID),
			cf.ID == "parent" && f.create.in.Parent != "": // a subtask's or child's, fixed
		case cf.Required:
			own = append(own, row(cf))
		case slices.Contains(createMoreKinds, cf.Kind):
			more = append(more, row(cf))
		}
	}
	if len(more) > 0 {
		label := i18n.Tf("+ %d more fields", len(more))
		if m.createMore {
			label = i18n.T("− fewer fields")
		}
		own = append(own, jiraFormField{FieldMeta: jira.FieldMeta{ID: createMoreField, Name: label}})
		if m.createMore {
			own = append(own, more...)
		}
	}
	f.fields = own
	f.idx = min(f.idx, len(f.fields))
}

// The create form's choices kept across restarts, in the site's state:
// the type last created per project, and whether the more fields show.
const (
	createTypeMeta = jiraMetaPrefix + "create_type:"
	createMoreMeta = jiraMetaPrefix + "create_more"
)

// lastCreateTypeIn is the type last created in project, "" for none.
func (m *Model) lastCreateTypeIn(project string) string {
	if t, ok := m.lastCreateType[project]; ok || m.store == nil {
		return t
	}
	t, _, _ := m.store.GetMeta(createTypeMeta + project)
	return t
}

// rememberCreateType keeps typ as the type last created in project.
func (m *Model) rememberCreateType(project, typ string) {
	if m.lastCreateType == nil {
		m.lastCreateType = map[string]string{}
	}
	m.lastCreateType[project] = typ
	if m.store != nil {
		_ = m.store.SetMeta(createTypeMeta+project, typ)
	}
}

// createMoreField is the create form's row that shows or hides the
// fields the type doesn't require.
const createMoreField = "_more"

// createMoreKinds are the fields the more-fields toggle offers: the ones
// the form edits. The sprint starts on the shown one and is joined after
// the create, as the board's sprint is.
var createMoreKinds = []string{jira.KindText, jira.KindStrings, jira.KindNumber, jira.KindDate, jira.KindTime, jira.KindDoc,
	jira.KindUser, jira.KindUsers, jira.KindOption, jira.KindOptions, jira.KindIssue, jira.KindSprint}

// toggleCreateMore shows or hides the create form's optional fields, what
// was typed in them kept, and remembers the choice for the next form.
func (m *Model) toggleCreateMore() {
	m.createMore = !m.createMore
	if m.store != nil {
		_ = m.store.SetMeta(createMoreMeta, strconv.FormatBool(m.createMore))
	}
	m.setCreateRows(m.jiraForm.create.screen)
	m.jiraForm.idx = slices.IndexFunc(m.jiraForm.fields, func(ff jiraFormField) bool { return ff.ID == createMoreField })
}

// formErrors files a refused create's reasons: those about a row under it
// (fieldErrs), the rest as the form's error, returned.
func formErrors(f *jiraFormState, err error) string {
	var re *jira.RequestError
	if !errors.As(err, &re) || len(re.Fields) == 0 {
		return err.Error()
	}
	f.create.fieldErrs = map[string]fieldErr{}
	rest := slices.Clone(re.Messages)
	for id, msg := range re.Fields {
		i := slices.IndexFunc(f.fields, func(ff jiraFormField) bool { return ff.ID == id })
		if i < 0 {
			rest = append(rest, id+": "+msg)
			continue
		}
		f.create.fieldErrs[id] = fieldErr{msg: msg, val: jiraValueText(f.fields[i].val)}
	}
	if len(rest) == 0 {
		return i18n.T("The site refused it: see the fields marked")
	}
	slices.Sort(rest[len(re.Messages):])
	return strings.Join(rest, "; ")
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// createFormType is the create form's type.
func createFormType(f *jiraFormState) string {
	for _, ff := range f.fields {
		if ff.ID == createTypeField && len(ff.val.Options) > 0 {
			return ff.val.Options[0].Name
		}
	}
	return ""
}

// syncCreateType follows a new type in the create form: the title (it may
// no longer join the sprint), the description while it is still the old
// type's template, and the fields the type requires.
func (m *Model) syncCreateType() tea.Cmd {
	f := m.jiraForm
	if f == nil || f.create == nil || !f.create.form {
		return nil
	}
	old, typ := m.jiraCreateType, createFormType(f)
	m.jiraCreateType = typ
	f.key = m.jiraCreateTitle()
	for i := range f.fields {
		if ff := &f.fields[i]; ff.ID == createDescField && ff.val.Text == m.opts.templates[strings.ToLower(old)] {
			ff.val.Text = m.opts.templates[strings.ToLower(typ)]
		}
	}
	return m.loadCreateFields()
}

// cycleCreateType steps the create form's type by d, as ← → do on its row.
func (m *Model) cycleCreateType(d int) tea.Cmd {
	ff := &m.jiraForm.fields[m.jiraForm.idx]
	i := slices.IndexFunc(ff.Options, func(o jira.Option) bool { return o.Name == createFormType(m.jiraForm) })
	o := ff.Options[(i+d+len(ff.Options))%len(ff.Options)]
	ff.val = jira.Value{Options: []jira.Option{o}}
	return m.syncCreateType()
}

// createFormIssue is the create form's issue: its own rows on the issue,
// fields the rest; the sprint when the shown one takes it.
func (m *Model) createFormIssue(f *jiraFormState, fields map[string]any) jiraFormCreate {
	cr := *f.create
	cr.in.Fields = maps.Clone(cr.in.Fields)
	if cr.in.Fields == nil {
		cr.in.Fields = map[string]any{}
	}
	maps.Copy(cr.in.Fields, fields)
	if !cr.form {
		return cr
	}
	for _, ff := range f.fields {
		switch ff.ID {
		case createTypeField:
			cr.in.Type = createFormType(f)
		case createSummaryField:
			cr.in.Summary = strings.TrimSpace(ff.val.Text)
		case createDescField:
			switch {
			case len(cr.in.DescriptionADF) > 0 && !ff.changed: // a clone's, untouched
			case len(cr.in.DescriptionADF) > 0:
				cr.in.DescriptionADF, _ = json.Marshal(jira.MarkdownToADFKept(ff.val.Text, cr.descKept))
			default:
				cr.in.Description = ff.val.Text
			}
		}
	}
	if cr.cloneOf == "" { // a clone stays out of the sprint, linked instead
		cr.sprint, _ = m.createSprint()
	}
	if id, ok := createFormSprint(f); ok {
		cr.sprint = id
	}
	cr.in.Mentions = cr.mentions
	return cr
}

// createFormSprint is the sprint the form's sprint row picked (0 the
// backlog); false without one.
func createFormSprint(f *jiraFormState) (int, bool) {
	for _, ff := range f.fields {
		if ff.Kind == jira.KindSprint {
			id := 0
			if len(ff.val.Options) > 0 {
				id, _ = strconv.Atoi(ff.val.Options[0].ID)
			}
			return id, true
		}
	}
	return 0, false
}

// createJiraIssue makes cr's issue: see createIssue.
func (m *Model) createJiraIssue(cr jiraFormCreate, title string) tea.Cmd {
	c, ctx := m.jiraClient, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, c.Scaled(30*time.Second))
		defer cancel()
		return createIssue(ctx, c, cr, title)
	}
}

// createIssue creates cr.in, adds it to cr.sprint (0 for none) and links a
// clone to its source. A create Jira refuses is checked against the create
// screen: required fields it lacks (a Component) come back as a form titled
// title; "" asks nothing.
func createIssue(ctx context.Context, c *jira.Client, cr jiraFormCreate, title string) jiraCreatedMsg {
	key, err := c.CreateIssue(ctx, cr.in)
	if err != nil && title != "" {
		if fields, fErr := c.CreateFields(ctx, cr.in.Project, cr.in.Type); fErr == nil {
			if form := buildCreateForm(title, cr, fields); form != nil {
				form.err = err.Error()
				return jiraCreatedMsg{err: err, form: form}
			}
		}
	}
	switch {
	case err != nil:
	case cr.sprint != 0:
		if err = c.MoveToSprint(ctx, cr.sprint, key); err != nil {
			err = &jiraCreateSprintErr{err}
		}
	case cr.cloneOf != "":
		err = c.LinkClone(ctx, key, cr.cloneOf)
	}
	return jiraCreatedMsg{key: key, err: err}
}

// buildCreateForm is the form for the required fields in lacks, nil when
// it lacks none.
func buildCreateForm(title string, cr jiraFormCreate, fields []jira.CreateField) *jiraFormState {
	in := cr.in
	set := map[string]bool{"project": true, "issuetype": true, "summary": true,
		"description": in.Description != "" || len(in.DescriptionADF) > 0, "parent": in.Parent != "", "priority": in.Priority != ""}
	if len(in.Labels) > 0 {
		set["labels"] = true
	}
	for id := range in.Fields {
		set[id] = true
	}
	f := &jiraFormState{key: title, create: &cr}
	for _, cf := range fields {
		if !cf.Required || set[cf.ID] {
			continue
		}
		f.fields = append(f.fields, jiraFormField{FieldMeta: cf.FieldMeta, required: true})
	}
	if len(f.fields) == 0 {
		return nil
	}
	return f
}

// jiraCreateSprintErr is a create that worked but did not reach the sprint.
type jiraCreateSprintErr struct{ err error }

func (e *jiraCreateSprintErr) Error() string {
	return i18n.Tf("not added to the sprint: %s", e.err.Error())
}

// handleJiraCreated opens the new issue and refetches the board; a refused
// create keeps the form.
func (m Model) handleJiraCreated(msg jiraCreatedMsg) (tea.Model, tea.Cmd) {
	if f := m.jiraForm; f != nil && f.create != nil && f.create.form && msg.key == "" {
		// Refused: the form stays with all typed, grown by the rows Jira
		// wants, its reasons under the fields they name.
		if msg.form != nil {
			for _, ff := range msg.form.fields {
				if !slices.ContainsFunc(f.fields, func(x jiraFormField) bool { return x.ID == ff.ID }) {
					f.fields = append(f.fields, ff)
				}
			}
		}
		f.busy, f.err = false, formErrors(f, msg.err)
		return m, nil
	}
	if f := m.jiraForm; f != nil && f.create != nil && f.create.form && f.create.another && msg.key != "" {
		return m.createdAnother(msg)
	}
	if f := m.jiraForm; f != nil && f.create != nil {
		m.jiraForm = nil
	}
	if msg.key == "" {
		m.fail(i18n.Tf("create: %s", msg.err.Error())) // the form was closed meanwhile
		return m, nil
	}
	refresh := m.refreshJiraAfterEdit()
	if m.jiraCreateReload && m.jiraTab.roadmap != nil {
		m.jiraCreateReload = false
		refresh = tea.Batch(refresh, m.loadRoadmap())
	}
	out, cmd := m.openJiraKey(msg.key)
	om := out.(Model)
	om.status = i18n.Tf("created %s", msg.key)
	if msg.err != nil {
		om.status += ", " + msg.err.Error()
	}
	return om, tea.Batch(cmd, refresh)
}

// createdAnother keeps the form after a create for the next one: its
// type, parent, sprint and fields stay, the summary and description start
// over, and the status line lists what it made.
func (m Model) createdAnother(msg jiraCreatedMsg) (tea.Model, tea.Cmd) {
	f := m.jiraForm
	f.busy, f.err, f.create.another = false, "", false
	f.create.made = append(f.create.made, msg.key)
	f.create.mentions, f.create.fieldErrs = nil, nil
	for i := range f.fields {
		switch ff := &f.fields[i]; ff.ID {
		case createSummaryField:
			ff.val, ff.changed = jira.Value{}, false
			f.idx = i
		case createDescField:
			ff.val, ff.changed = jira.Value{Text: m.opts.templates[strings.ToLower(createFormType(f))]}, false
		}
	}
	m.status = i18n.Tf("created %s · %d made: %s", msg.key, len(f.create.made), strings.Join(f.create.made, ", "))
	if msg.err != nil {
		m.status += " · " + msg.err.Error()
	}
	return m, tea.Batch(m.editJiraFormField(), m.refreshJiraAfterEdit())
}

// createSprint is the sprint a new issue joins: the shown sprint view's,
// for an issue without a parent that is no epic; 0 for none.
func (m *Model) createSprint() (int, string) {
	if v, ok := m.jiraCurrentView(); ok && v.kind == jiraViewSprint && m.jiraCreateParent == "" && m.jiraCreateClone == "" && !strings.EqualFold(m.jiraCreateType, "epic") {
		return v.sprint, v.name
	}
	return 0, ""
}

// jiraCreateTitle is "New Bug in ABC", "New Bug in ABC → Sprint 12" (it
// joins the sprint shown), or "New Sub-task of ABC-1".
func (m *Model) jiraCreateTitle() string {
	if m.jiraCreateClone != "" {
		return i18n.Tf("Clone of %s · %s", m.jiraCreateClone, m.jiraCreateType)
	}
	if m.jiraCreateParent != "" {
		return i18n.Tf("New %s of %s", m.jiraCreateType, m.jiraCreateParent)
	}
	id, name := m.createSprint()
	if f := m.jiraForm; f != nil && f.create != nil {
		for _, ff := range f.fields {
			if ff.Kind == jira.KindSprint { // the sprint row says where it goes
				id, name = 0, ""
				if len(ff.val.Options) > 0 && ff.val.Options[0].ID != "" {
					id, name = 1, ff.val.Options[0].Name
				}
			}
		}
	}
	if id != 0 {
		return i18n.Tf("New %s in %s → %s", m.jiraCreateType, m.jiraTab.project, name)
	}
	return i18n.Tf("New %s in %s", m.jiraCreateType, m.jiraTab.project)
}
