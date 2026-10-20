FROM public.ecr.aws/docker/library/node:20-alpine AS build
WORKDIR /app
COPY package.json ./
RUN npm install --omit=dev
COPY src ./src
RUN npx tsc --outDir dist src/*.ts

FROM gcr.io/distroless/nodejs20
WORKDIR /app
COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist
CMD ["dist/handler.js"]
