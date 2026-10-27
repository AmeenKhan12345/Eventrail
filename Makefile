.PHONY: migrate-up migrate-down create-test-db test

# Apply every migration that has not run yet. Requires DATABASE_URL.
migrate-up:
	migrate -path migrations -database "$(DATABASE_URL)" up

# Roll back only the latest migration. Requires DATABASE_URL.
migrate-down:
	migrate -path migrations -database "$(DATABASE_URL)" down 1

# Create the empty webhooks_test database used by store tests. Requires DATABASE_URL.
create-test-db:
	psql "$(DATABASE_URL)" -tc "SELECT 1 FROM pg_database WHERE datname = 'webhooks_test'" | grep -q 1 || psql "$(DATABASE_URL)" -c "CREATE DATABASE webhooks_test"

# Run all tests. Requires TEST_DATABASE_URL.
test:
	@test -n "$(TEST_DATABASE_URL)" || (echo "TEST_DATABASE_URL is required" && exit 1)
	go test -p 1 ./...
