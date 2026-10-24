const BASE = (import.meta.env.VITE_API_URL as string | undefined) ?? "http://localhost:8080";

export type Role = "admin" | "manager" | "waiter" | "cashier" | "kitchen";
export interface User { id: number; fullName: string; username: string; role: Role }
export interface Option { id: number; name: string; priceSantim: number; available: boolean }
export interface MenuItem { id: number; categoryId: number; name: string; description: string; priceSantim: number; available: boolean; variants: Option[]; extras: Option[] }
export interface Category { id: number; name: string; items: MenuItem[] }
export interface OrderItem { id: number; name: string; variantName: string; quantity: number; lineTotalSantim: number; note: string; kitchenStatus: string; options: { name: string; priceSantim: number }[] }
export interface Order {
  id: number; orderNumber: number; source: string; orderType: string; tableLabel: string; customerName: string; customerPhone?: string;
  deliveryAddress?: string; notes: string; status: string; paymentStatus: string; totalSantim: number; createdAt: string; items: OrderItem[];
}
export interface Table { id: number; label: string; seats: number; occupied: boolean }
export interface Summary { days: number; orderCount: number; paidOrderCount: number; paidSalesSantim: number; averagePaidOrderSantim: number; topItems: { name: string; quantity: number; revenueSantim: number }[] }

export class ApiError extends Error { constructor(public status: number, message: string) { super(message); } }

let token: string | null = null;
let onUnauthorized: () => void = () => {};
export const setToken = (t: string | null) => { token = t; };
export const setUnauthorizedHandler = (f: () => void) => { onUnauthorized = f; };

async function call<T>(method: string, path: string, body?: unknown, idem?: string): Promise<T> {
  let res: Response;
  try {
    res = await fetch(BASE + path, {
      method, body: body === undefined ? undefined : JSON.stringify(body),
      headers: { "Content-Type": "application/json", ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(idem ? { "Idempotency-Key": idem } : {}) },
    });
  } catch { throw new ApiError(0, "Cannot reach the server. Check the connection."); }
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    if (res.status === 401 && token) onUnauthorized();
    throw new ApiError(res.status, (data as { error?: { message: string } }).error?.message ?? "Something went wrong.");
  }
  return data as T;
}

export const api = {
  login: (username: string, password: string) => call<{ token: string; user: User }>("POST", "/api/v1/auth/login", { username, password }),
  logout: () => call<void>("POST", "/api/v1/auth/logout"),
  menu: () => call<{ categories: Category[] }>("GET", "/api/v1/staff/menu"),
  patchItem: (id: number, p: { priceSantim?: number; available?: boolean }) => call<void>("PATCH", `/api/v1/staff/menu/items/${id}`, p),
  createItem: (p: { categoryId: number; name: string; description: string; priceSantim: number }) => call<{ id: number }>("POST", "/api/v1/staff/menu/items", p),
  tables: () => call<{ tables: Table[] }>("GET", "/api/v1/staff/tables"),
  orders: (all = false) => call<{ orders: Order[] }>("GET", `/api/v1/staff/orders${all ? "?scope=all" : ""}`),
  createOrder: (p: unknown, key: string) => call<Order>("POST", "/api/v1/staff/orders", p, key),
  setStatus: (id: number, status: string, reason = "") => call<Order>("POST", `/api/v1/staff/orders/${id}/status`, { status, reason }),
  kitchenItem: (id: number, itemId: number, status: string) => call<Order>("POST", `/api/v1/staff/orders/${id}/items/${itemId}/kitchen-status`, { status }),
  payCash: (id: number) => call<Order>("POST", `/api/v1/staff/orders/${id}/payments/cash`),
  summary: (days: number) => call<Summary>("GET", `/api/v1/staff/reports/summary?days=${days}`),
};

/** Which screens each role may open. The server enforces the same rules; this only hides unusable screens. */
export const ROLE_SCREENS: Record<Role, string[]> = {
  admin: ["dashboard", "waiter", "kitchen", "cashier", "menu"],
  manager: ["dashboard", "waiter", "kitchen", "cashier", "menu"],
  waiter: ["waiter"],
  cashier: ["cashier"],
  kitchen: ["kitchen"],
};
