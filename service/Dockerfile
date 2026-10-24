FROM golang:1.27.3-bookworm AS build

WORKDIR /src
ENV GOTOOLCHAIN=local
COPY go.mod *.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/ghreport . \
    && mkdir -p /out/work

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/ghreport /ghreport
COPY --from=build --chown=65532:65532 /out/work/ /work/

USER 65532:65532
WORKDIR /work
ENTRYPOINT ["/ghreport"]
