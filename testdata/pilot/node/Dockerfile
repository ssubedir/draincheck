FROM node:24-alpine

WORKDIR /app
COPY server.js ./server.js

EXPOSE 8080
USER node
ENTRYPOINT ["node", "/app/server.js"]
