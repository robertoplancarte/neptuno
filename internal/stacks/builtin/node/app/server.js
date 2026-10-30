import { createServer } from "node:http";

const port = process.env.PORT || {{.Port}};

createServer((req, res) => {
  res.writeHead(200, { "Content-Type": "text/plain" });
  res.end("Hello from {{.Name}}\n");
}).listen(port, () => console.log(`{{.Name}} listening on ${port}`));
