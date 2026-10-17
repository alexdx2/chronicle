import axios from 'axios';

const payments = axios.create({
  timeout: 3000,
  baseURL: 'https://api.payments.io/v1',
});

const local = axios.create({ baseURL: 'http://localhost:3000' });
// const old = axios.create({ baseURL: 'https://commented.example.com' });

export async function load() {
  const r = await fetch('https://feed.example.com/items');
  const t = await fetch(`http://${process.env.X}/y`);
  const u = await fetch(`https://${HOST}/z`);
  const rel = await fetch('/api/local');
  const v = await fetch(`${TOM_API_URL}/tom/status`);
  const w = await fetch(`${prefix}/x`);
  const bare = await fetch(`${url}/x`);
  // const old = await fetch(`${OLD_URL}/x`);
  const cc = await fetch(`${apiBaseUrl}/items?a=1`);
  // await fetch('https://comment.example.com');
  return r;
}

export class Svc {
  constructor(private http: HttpService) {}
  go() {
    return this.http.get('https://svc.example.com/health');
  }
  post() {
    return this.httpService.post("https://svc2.example.com/x", {});
  }
}
