# syntax=docker/dockerfile:1
# One image for the live demo: the Go API serves the contract and the built POS UI
# from the same origin, so there is no CORS to configure. See docs/deploy.md.

FROM docker.io/library/node:26-alpine AS web
WORKDIR /src
COPY . .
# Node 25 and later ship without corepack, so pnpm comes from npm at the exact
# version package.json's packageManager names.
RUN npm install --global "$(node -p "require('./package.json').packageManager")" \
 && pnpm install --frozen-lockfile --filter "@modura/pos-web..." \
 && pnpm --filter @modura/pos-web run build

FROM docker.io/library/golang:1.27-alpine AS api
WORKDIR /src/apps/sales-go
COPY apps/sales-go/go.mod apps/sales-go/go.sum ./
RUN go mod download
COPY apps/sales-go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sales-api ./cmd/sales-api

FROM gcr.io/distroless/static-debian13:nonroot
WORKDIR /app
COPY --from=api /out/sales-api /app/sales-api
COPY db /app/db
COPY --from=web /src/apps/pos-web/dist /app/web
ENV MODURA_DB_DIR=/app/db \
    MODURA_WEB_DIR=/app/web \
    PORT=8080
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/app/sales-api"]
CMD ["serve"]
