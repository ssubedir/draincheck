FROM python:3.14-alpine

WORKDIR /app
COPY server.py ./server.py

EXPOSE 8080
USER nobody
ENTRYPOINT ["python", "/app/server.py"]
