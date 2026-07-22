package main

import (
	"net/http"

	"studdle/backend/api/handler"
	"studdle/backend/internal/http/middleware"
)

// buildRouter constructs the ServeMux and wraps it with the global middleware stack.
func buildRouter(d *deps) http.Handler {
	mux := http.NewServeMux()

	authMW := middleware.Auth(d.signer)
	verifiedMW := middleware.RequireVerified()
	auth := wrap(authMW)
	av := wrap(authMW, verifiedMW)
	admMW := middleware.RequireAdmin()
	adm := wrap(authMW, verifiedMW, admMW)

	registerPublicRoutes(mux, d)
	registerAuthReadRoutes(mux, d, auth)
	registerAuthSocialRoutes(mux, d, auth)
	registerVerifiedRoutes(mux, d, av)
	registerStubRoutes(mux, d, av)
	registerAdminRoutes(mux, d, adm)

	stack := middleware.Chain(
		middleware.Recoverer(),
		middleware.RequestID(),
		middleware.CORS(d.cfg.CORSOrigins...),
		middleware.Logger(),
	)
	return stack(mux)
}

// wrap returns a helper that attaches the given middlewares (outer→inner) to a HandlerFunc.
func wrap(mws ...middleware.Middleware) func(http.HandlerFunc) http.Handler {
	m := middleware.Chain(mws...)
	return func(h http.HandlerFunc) http.Handler { return m(h) }
}

// registerPublicRoutes attaches routes that require no authentication.
func registerPublicRoutes(mux *http.ServeMux, d *deps) {
	userH := handler.NewUserHandler(d.user, d.emailVer)
	emailVerH := handler.NewEmailVerificationHandler(d.emailVer, d.user)
	imgH := handler.NewImageHandler(d.image)
	billH := handler.NewBillingHandler(d.billing, d.user, d.prices, d.cfg.AppURL+"/billing", d.cfg.AppURL+"/pricing")
	billH.SetStripeLivemode(d.cfg.StripeMode == "live")

	docsH := handler.NewDocsHandler()

	mux.HandleFunc("POST /user-register", userH.Register)
	mux.HandleFunc("POST /user-login", userH.Login)
	mux.HandleFunc("GET /verify-email", emailVerH.Verify)
	mux.HandleFunc("GET /images/{id}", imgH.Serve)
	mux.HandleFunc("POST /billing/webhook", billH.Webhook)
	mux.HandleFunc("GET /billing/plans", billH.GetPlans)
	mux.HandleFunc("GET /docs", docsH.UI)
	mux.HandleFunc("GET /openapi.yaml", docsH.Spec)
}

// registerAuthReadRoutes attaches authenticated read / query routes (no email-verification gate).
func registerAuthReadRoutes(mux *http.ServeMux, d *deps, auth func(http.HandlerFunc) http.Handler) {
	userH := handler.NewUserHandler(d.user, d.emailVer)
	emailVerH := handler.NewEmailVerificationHandler(d.emailVer, d.user)
	subjH := handler.NewSubjectHandler(d.subject)
	chapH := handler.NewChapterHandler(d.chapter)
	fcH := handler.NewFlashcardHandler(d.flashcard)
	searchH := handler.NewSearchHandler(d.search)
	aiH := handler.NewAIHandler(d.ai)

	mux.Handle("POST /user-test-jwt", auth(userH.TestJWT))
	mux.Handle("POST /resend-verification", auth(emailVerH.Resend))
	mux.Handle("GET /subject-list", auth(subjH.List))
	mux.Handle("GET /subject", auth(subjH.Get))
	mux.Handle("GET /subject-stats", auth(subjH.Stats))
	mux.Handle("GET /subject-stats-history", auth(subjH.History))
	mux.Handle("GET /subject-stats-mastery-trend", auth(subjH.MasteryTrend))
	mux.Handle("GET /chapter-list", auth(chapH.List))
	mux.Handle("GET /chapter-stats", auth(chapH.Stats))
	mux.Handle("GET /flashcard-list", auth(fcH.ListBySubject))
	mux.Handle("GET /flashcard", auth(fcH.Get))
	mux.Handle("POST /flashcard-review", auth(fcH.Review))
	mux.Handle("GET /search/subjects/owned", auth(searchH.SubjectsOwned))
	mux.Handle("GET /search/subjects/public", auth(searchH.SubjectsPublic))
	mux.Handle("GET /search/users", auth(searchH.Users))
	mux.Handle("GET /search/flashcards", auth(searchH.Flashcards))
	mux.Handle("GET /ai/quota", auth(aiH.Quota))
}

// registerAuthSocialRoutes attaches authenticated social, settings, and billing routes.
func registerAuthSocialRoutes(mux *http.ServeMux, d *deps, auth func(http.HandlerFunc) http.Handler) {
	friendH := handler.NewFriendshipHandler(d.friendship)
	subsH := handler.NewSubjectSubscriptionHandler(d.subjectSub)
	collabH := handler.NewCollaborationHandler(d.collab)
	prefH := handler.NewPreferencesHandler(d.preferences)
	gamH := handler.NewGamificationHandler(d.gamification)
	billH := handler.NewBillingHandler(d.billing, d.user, d.prices, d.cfg.AppURL+"/billing", d.cfg.AppURL+"/pricing")
	billH.SetStripeLivemode(d.cfg.StripeMode == "live")

	mux.Handle("POST /friendship-accept", auth(friendH.Accept))
	mux.Handle("POST /friendship-decline", auth(friendH.Decline))
	mux.Handle("POST /friendship-unfriend", auth(friendH.Unfriend))
	mux.Handle("GET /friendship-list", auth(friendH.ListFriends))
	mux.Handle("GET /friendship-pending", auth(friendH.ListPending))
	mux.Handle("POST /subject-subscribe", auth(subsH.Subscribe))
	mux.Handle("POST /subject-unsubscribe", auth(subsH.Unsubscribe))
	mux.Handle("GET /subject-subscriptions", auth(subsH.List))
	mux.Handle("GET /collaborators", auth(collabH.ListCollaborators))
	mux.Handle("GET /preferences", auth(prefH.Get))
	mux.Handle("POST /preferences-update", auth(prefH.Update))
	mux.Handle("GET /gamification-state", auth(gamH.State))
	mux.Handle("POST /training-session-record", auth(gamH.RecordSession))
	mux.Handle("GET /user-stats", auth(gamH.Stats))
	mux.Handle("GET /achievements", auth(gamH.Achievements))
	mux.Handle("GET /billing/subscription", auth(billH.GetSubscription))
	mux.Handle("POST /billing/checkout", auth(billH.Checkout))
	mux.Handle("POST /billing/portal", auth(billH.Portal))
	mux.Handle("POST /billing/refresh", auth(billH.Refresh))
}

// registerVerifiedRoutes attaches routes that require authentication and email verification.
func registerVerifiedRoutes(mux *http.ServeMux, d *deps, av func(http.HandlerFunc) http.Handler) {
	userH := handler.NewUserHandler(d.user, d.emailVer)
	imgH := handler.NewImageHandler(d.image)
	subjH := handler.NewSubjectHandler(d.subject)
	chapH := handler.NewChapterHandler(d.chapter)
	fcH := handler.NewFlashcardHandler(d.flashcard)
	friendH := handler.NewFriendshipHandler(d.friendship)
	collabH := handler.NewCollaborationHandler(d.collab)
	examH := handler.NewExamHandler(d.exam)
	planH := handler.NewRevisionPlanHandler(d.plan)

	mux.Handle("POST /set-profile-picture", av(userH.SetProfilePicture))
	mux.Handle("GET /get-user-stats", av(userH.Stats))
	mux.Handle("POST /upload-image", av(imgH.Upload))
	mux.Handle("POST /delete-image", av(imgH.Delete))
	mux.Handle("POST /subject-create", av(subjH.Create))
	mux.Handle("POST /subject-update", av(subjH.Update))
	mux.Handle("POST /subject-delete", av(subjH.Delete))
	mux.Handle("POST /chapter-create", av(chapH.Create))
	mux.Handle("POST /chapter-update", av(chapH.Update))
	mux.Handle("POST /chapter-delete", av(chapH.Delete))
	mux.Handle("POST /flashcard-create", av(fcH.Create))
	mux.Handle("POST /flashcard-update", av(fcH.Update))
	mux.Handle("POST /flashcard-delete", av(fcH.Delete))
	mux.Handle("POST /friendship-request", av(friendH.Request))
	mux.Handle("POST /collaborators", av(collabH.AddCollaborator))
	mux.Handle("POST /collaborator-remove", av(collabH.RemoveCollaborator))
	mux.Handle("POST /collaboration-invites", av(collabH.CreateInvite))
	mux.Handle("POST /collaboration-invite-redeem", av(collabH.RedeemInvite))
	mux.Handle("POST /collaboration-invite-revoke", av(collabH.RevokeInvite))
	mux.Handle("POST /exams", av(examH.Create))
	mux.Handle("GET /exams", av(examH.List))
	mux.Handle("GET /exams/{id}", av(examH.Get))
	mux.Handle("PUT /exams/{id}", av(examH.Update))
	mux.Handle("DELETE /exams/{id}", av(examH.Delete))
	mux.Handle("POST /exams/{id}/generate-plan", av(planH.Generate))
	mux.Handle("GET /exams/{id}/plan", av(planH.Get))
	mux.Handle("POST /exams/{id}/mark-done", av(planH.MarkDone))
}

// registerStubRoutes attaches AI, quiz, and duel stub routes (auth + verified).
// Plan endpoints have moved to registerVerifiedRoutes; Spec B replaced the stubs.
func registerStubRoutes(mux *http.ServeMux, d *deps, av func(http.HandlerFunc) http.Handler) {
	aiH := handler.NewAIHandler(d.ai)
	quizH := handler.NewQuizHandler(d.quiz)
	duelH := handler.NewDuelHandler(d.duel)

	mux.Handle("POST /ai/flashcards/prompt", av(aiH.GenerateFromPrompt))
	mux.Handle("POST /ai/flashcards/pdf", av(aiH.GenerateFromPDF))
	mux.Handle("POST /ai/check", av(aiH.Check))
	mux.Handle("POST /ai/commit-generation", av(aiH.CommitGeneration))
	mux.Handle("POST /quiz/generate", av(quizH.Generate))
	mux.Handle("POST /quiz/attempt", av(quizH.Attempt))
	mux.Handle("POST /quiz/share", av(quizH.Share))
	mux.Handle("POST /duel/invite", av(duelH.Invite))
	mux.Handle("POST /duel/accept", av(duelH.Accept))
	mux.Handle("GET /duel/connect", av(duelH.Connect))
}

// registerAdminRoutes attaches admin-only routes (auth + verified + admin).
func registerAdminRoutes(mux *http.ServeMux, d *deps, adm func(http.HandlerFunc) http.Handler) {
	adminAIH := handler.NewAdminAIHandler(d.billing, d.access)
	mux.Handle("POST /admin/grant-ai-access", adm(adminAIH.GrantAIAccess))

	adminBillH := handler.NewAdminBillingHandler(d.billing, d.access)
	mux.Handle("POST /admin/comp-subscription", adm(adminBillH.Grant))
	mux.Handle("DELETE /admin/comp-subscription", adm(adminBillH.Revoke))
}
