export type OrderId = string;

export type OrderStatus = 'open' | 'paid' | 'void';

export interface Order {
  id: OrderId;
  status: OrderStatus;
  total: number;
}
