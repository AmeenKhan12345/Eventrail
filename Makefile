.PHONY: migrate-up migrate-down

# Apply every migration that has not run yet. Requires DATABASE_URL.
migrate-up:
	migrate -path migrations -database "$(DATABASE_URL)" up

# Roll back only the latest migration. Requires DATABASE_URL.
migrate-down:
	migrate -path migrations -database "$(DATABASE_URL)" down 1
