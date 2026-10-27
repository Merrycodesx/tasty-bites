const BASE = (import.meta.env.VITE_API_URL as string | undefined) ?? "http://localhost:8080";

export interface Option { id: number; name: string; priceSantim: number; available: boolean }
export interface MenuItem {
  id: number; categoryId: number; name: string; description: string; priceSantim: number;
  available: boolean; variants: Option[]; extras: Option[];
}
export interface Category { id: number; name: string; items: MenuItem[] }
export interface TableInfo { table: { label: string }; restaurant: { name: string } }
export interface OrderItem {
  id: number; name: string; variantName: string; quantity: number; lineTotalSantim: number;
  note: string; kitchenStatus: string; options: { name: string; priceSantim: number }[];
}
export interface Order {
  id: number; orderNumber: number; publicToken?: string; orderType: string; tableLabel: string;
  status: string; paymentStatus: string; totalSantim: number; createdAt: string; items: OrderItem[];
}
export interface OrderRequest {
  orderType: "dine_in" | "takeaway" | "delivery";
  tableToken?: string; customerName: string; customerPhone: string; deliveryAddress: string; notes: string;
  items: { menuItemId: number; variantId?: number; extraIds: number[]; quantity: number; note: string }[];
}

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) { super(message); }
}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(BASE + path, { ...init, headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) } });
  } catch {
    throw new ApiError(0, "network", "Could not reach the restaurant. Check your connection and try again.");
  }
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    const e = (body as { error?: { code: string; message: string } }).error;
    throw new ApiError(res.status, e?.code ?? "error", e?.message ?? "Something went wrong. Please try again.");
  }
  return body as T;
}

export const getMenu = () => call<{ categories: Category[] }>("/api/v1/menu");
export const resolveTable = (token: string) => call<TableInfo>(`/api/v1/tables/resolve?t=${encodeURIComponent(token)}`);
export const API_BASE = BASE;
export const trackOrder = (token: string) => call<Order>(`/api/v1/orders/track/${encodeURIComponent(token)}`);
export const placeOrder = (req: OrderRequest, idempotencyKey: string) =>
  call<Order>("/api/v1/orders", { method: "POST", body: JSON.stringify(req), headers: { "Idempotency-Key": idempotencyKey } });
