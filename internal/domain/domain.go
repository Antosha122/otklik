package domain

import (
	"errors"

	"github.com/google/uuid"
)

type Role string

const (
	RoleApplicant Role = "applicant"
	RoleOperator  Role = "operator"
	RoleExpert    Role = "expert"
	RoleAdmin     Role = "admin"
)

type Status string

const (
	StatusNew                Status = "new"
	StatusAssigned           Status = "assigned"
	StatusInProgress         Status = "in_progress"
	StatusNeedsClarification Status = "needs_clarification"
	StatusAnswerReady        Status = "answer_ready"
	StatusCompleted          Status = "completed"
	StatusReturned           Status = "returned"
	StatusRejected           Status = "rejected"
	StatusClosedNoResponse   Status = "closed_no_response"
)

var allStatuses = map[Status]bool{
	StatusNew: true, StatusAssigned: true, StatusInProgress: true,
	StatusNeedsClarification: true, StatusAnswerReady: true, StatusCompleted: true,
	StatusReturned: true, StatusRejected: true, StatusClosedNoResponse: true,
}

func (s Status) Valid() bool { return allStatuses[s] }

func (s Status) Terminal() bool {
	return s == StatusCompleted || s == StatusRejected || s == StatusClosedNoResponse
}

var Transitions = map[Status]map[Status]bool{
	StatusNew: {
		StatusAssigned:  true,
		StatusRejected:  true,
		StatusCompleted: true,
	},
	StatusAssigned: {
		StatusInProgress: true,
	},
	StatusInProgress: {
		StatusNeedsClarification: true,
		StatusAnswerReady:        true,
	},
	StatusNeedsClarification: {
		StatusInProgress:       true,
		StatusClosedNoResponse: true,
	},
	StatusAnswerReady: {
		StatusCompleted:        true,
		StatusReturned:         true,
		StatusClosedNoResponse: true,
	},
	StatusReturned: {
		StatusAssigned: true,
		StatusRejected: true,
	},
}

func CanTransition(from, to Status) bool {
	if from == to || !to.Valid() {
		return false
	}
	allowed, ok := Transitions[from]
	return ok && allowed[to]
}

type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityUrgent Priority = "urgent"
)

func (p Priority) Valid() bool {
	return p == PriorityLow || p == PriorityNormal || p == PriorityUrgent
}

type ApplicantType string

const (
	ApplicantSchoolchild ApplicantType = "schoolchild"
	ApplicantParent      ApplicantType = "parent"
	ApplicantTeacher     ApplicantType = "teacher"
)

func (t ApplicantType) Valid() bool {
	return t == ApplicantSchoolchild || t == ApplicantParent || t == ApplicantTeacher
}

// Principal — субъект доступа; у заявителя вместо аккаунта —
// короткоживая сессия по трек-номеру, привязанная к одному обращению.
type Principal struct {
	Role     Role
	UserID   uuid.UUID
	Login    string
	AppealID uuid.UUID
	// MustChangePassword — у сотрудника ещё не сменён демо-пароль:
	// API закрыт, кроме смены пароля и логаута.
	MustChangePassword bool
}

func (p Principal) IsStaff() bool {
	return p.Role == RoleOperator || p.Role == RoleExpert || p.Role == RoleAdmin
}

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrValidation   = errors.New("validation error")
	ErrRateLimited  = errors.New("rate limited")
	// ErrReturnLimitReached — заявитель исчерпал лимит возвратов (ТЗ 5.1).
	ErrReturnLimitReached = errors.New("return limit reached")
)

type IntakeQuestion struct {
	Text    string   `json:"text"`
	Options []string `json:"options"`
}

var IntakeQuestions = []IntakeQuestion{
	{Text: "Где это происходит?", Options: []string{
		"В школе", "В интернете или соцсетях", "И там, и там", "Другое место"}},
	{Text: "Как давно это происходит?", Options: []string{
		"Только что", "Несколько дней", "Несколько недель", "Больше месяца"}},
	{Text: "Кто участвует?", Options: []string{
		"Одноклассники", "Учитель", "Родители или семья", "Незнакомые люди", "Несколько людей"}},
	{Text: "Обращался ли кто-то уже за помощью?", Options: []string{
		"Нет, это первое обращение", "Да, говорили взрослым", "Да, уже обращались сюда"}},
}

const MaxAttachmentsPerAppeal = 5

const MaxAttachmentSizeBytes = 10 << 20

const (
	QueueOverdueHours    = 24
	ResponseOverdueHours = 24
)
