package user

const (
	qInsertUser = `
        INSERT INTO users (username, email, password_hash)
        VALUES ($1, $2, $3)
        RETURNING id, created_at
    `

	qFindByIdentifier = `
        SELECT id, username, email, password_hash, email_verified, is_admin, created_at
        FROM users
        WHERE username = $1 OR email = $1
    `

	qFindByID = `
        SELECT id, username, email, email_verified, is_admin, created_at
        FROM users
        WHERE id = $1
    `

	qSetProfilePicture = `
        UPDATE users SET profile_picture_image_id = $2, updated_at = now()
        WHERE id = $1
    `

	qStats = `
        SELECT
            COUNT(*)                                       AS total,
            COUNT(*) FILTER (WHERE f.last_result = 2)      AS good,
            COUNT(*) FILTER (WHERE f.last_result = 1)      AS ok,
            COUNT(*) FILTER (WHERE f.last_result = 0)      AS bad,
            COUNT(*) FILTER (WHERE f.last_result = -1)     AS new_count
        FROM flashcards f
        JOIN subjects s ON s.id = f.subject_id
        WHERE s.owner_id = $1
    `

	qAchievementProgress = `
        SELECT COUNT(*) FROM unlocked_achievements WHERE user_id = $1
    `
)
