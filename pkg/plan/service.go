package plan

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"studdle/backend/pkg/access"
	"studdle/backend/pkg/aipipeline"
	"studdle/backend/pkg/exam"
	"studdle/backend/pkg/image"
)

// Service is the revision-plan facade. It composes the AI pipeline, the exam
// service, and the access service to produce + persist + read plans.
type Service struct {
	db     *pgxpool.Pool       // db is the shared pool
	ai     *aipipeline.Service // ai owns AI calls and quotas
	exam   *exam.Service       // exam answers ownership and read-back questions
	image  *image.Service      // image streams annales PDFs from disk
	access *access.Service     // access answers entitlement / subject-permission questions
	model  string              // model identifies the AI model used (echoed into revision_plans.model)
}

// NewService constructs the plan Service.
func NewService(db *pgxpool.Pool, ai *aipipeline.Service, examSvc *exam.Service, imgSvc *image.Service, acc *access.Service, model string) *Service {
	return &Service{db: db, ai: ai, exam: examSvc, image: imgSvc, access: acc, model: model}
}
