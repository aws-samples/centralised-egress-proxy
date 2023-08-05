FROM public.ecr.aws/docker/library/golang:alpine as build

WORKDIR /app

COPY go.mod ./
COPY go.sum ./

RUN go mod download

COPY *.go ./

RUN go build -o ./placeholder-binary

FROM public.ecr.aws/docker/library/alpine:latest

RUN apk add --no-cache py3-botocore curl

WORKDIR /app

COPY --from=build /app/placeholder-binary .

CMD [ "/app/placeholder-binary" ]