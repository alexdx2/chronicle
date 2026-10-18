import { useRouter } from 'next/router';
import { api } from '../lib/api';

export default function Page() {
  const router = useRouter();
  const expression = 1;
  api.get('/api/orders');
  api.post('/api/orders/refresh');
  return null;
}
