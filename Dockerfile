FROM gcr.io/distroless/static:latest
WORKDIR /
COPY tfxunpack tfxunpack

ENTRYPOINT ["/tfxunpack"]
