# Dev-mode image (runs the Vite dev server). Replace with a static
# nginx-served production build before shipping to a customer.
FROM node:22-alpine

WORKDIR /app
COPY package.json ./
RUN npm install
COPY . .

EXPOSE 5173
CMD ["npm", "run", "dev"]
