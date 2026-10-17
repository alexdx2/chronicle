const server = Fastify();
server.get('/health', h);
server.get(`/files/${name}`, h);
