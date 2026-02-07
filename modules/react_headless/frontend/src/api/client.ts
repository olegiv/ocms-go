import type {
  ListResponse,
  SingleResponse,
  Page,
  Tag,
  Category,
  Media,
} from '../types';

// Configuration - update these for your environment
const API_BASE_URL = import.meta.env.VITE_API_URL || '/api/v1';
const API_KEY = import.meta.env.VITE_API_KEY || '';

async function apiFetch<T>(path: string, options?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    'Accept': 'application/json',
  };

  if (API_KEY) {
    headers['Authorization'] = `Bearer ${API_KEY}`;
  }

  const response = await fetch(`${API_BASE_URL}${path}`, {
    ...options,
    headers: {
      ...headers,
      ...options?.headers,
    },
  });

  if (!response.ok) {
    const errorBody = await response.json().catch(() => null);
    const message = errorBody?.error?.message || `API error: ${response.status}`;
    throw new Error(message);
  }

  return response.json();
}

// Pages
export async function fetchPages(params?: {
  page?: number;
  per_page?: number;
  status?: string;
  category?: number;
  tag?: number;
  include?: string;
}): Promise<ListResponse<Page>> {
  const searchParams = new URLSearchParams();
  if (params?.page) searchParams.set('page', String(params.page));
  if (params?.per_page) searchParams.set('per_page', String(params.per_page));
  if (params?.status) searchParams.set('status', params.status);
  if (params?.category) searchParams.set('category', String(params.category));
  if (params?.tag) searchParams.set('tag', String(params.tag));
  if (params?.include) searchParams.set('include', params.include);

  const query = searchParams.toString();
  return apiFetch<ListResponse<Page>>(`/pages${query ? `?${query}` : ''}`);
}

export async function fetchPageBySlug(slug: string): Promise<SingleResponse<Page>> {
  return apiFetch<SingleResponse<Page>>(`/pages/slug/${encodeURIComponent(slug)}?include=author,categories,tags`);
}

export async function fetchPageById(id: number): Promise<SingleResponse<Page>> {
  return apiFetch<SingleResponse<Page>>(`/pages/${id}?include=author,categories,tags`);
}

// Tags
export async function fetchTags(params?: {
  page?: number;
  per_page?: number;
}): Promise<ListResponse<Tag>> {
  const searchParams = new URLSearchParams();
  if (params?.page) searchParams.set('page', String(params.page));
  if (params?.per_page) searchParams.set('per_page', String(params.per_page));

  const query = searchParams.toString();
  return apiFetch<ListResponse<Tag>>(`/tags${query ? `?${query}` : ''}`);
}

// Categories
export async function fetchCategories(): Promise<ListResponse<Category>> {
  return apiFetch<ListResponse<Category>>('/categories');
}

// Media
export async function fetchMedia(params?: {
  page?: number;
  per_page?: number;
  type?: string;
  include?: string;
}): Promise<ListResponse<Media>> {
  const searchParams = new URLSearchParams();
  if (params?.page) searchParams.set('page', String(params.page));
  if (params?.per_page) searchParams.set('per_page', String(params.per_page));
  if (params?.type) searchParams.set('type', params.type);
  if (params?.include) searchParams.set('include', params.include);

  const query = searchParams.toString();
  return apiFetch<ListResponse<Media>>(`/media${query ? `?${query}` : ''}`);
}
