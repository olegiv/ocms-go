// oCMS API response types

export interface PaginationMeta {
  total: number;
  page: number;
  per_page: number;
  pages: number;
}

export interface ListResponse<T> {
  data: T[];
  meta: PaginationMeta;
}

export interface SingleResponse<T> {
  data: T;
}

export interface APIError {
  error: {
    code: string;
    message: string;
    details?: Record<string, string>;
  };
}

export interface Page {
  id: number;
  title: string;
  slug: string;
  body: string;
  status: 'draft' | 'published';
  page_type: string;
  author_id: number;
  language_code: string;
  created_at: string;
  updated_at: string;
  published_at: string | null;
  featured_image_id: number | null;
  meta_title: string;
  meta_description: string;
  author?: Author;
  categories?: Category[];
  tags?: Tag[];
}

export interface Author {
  id: number;
  display_name: string;
  email: string;
}

export interface Tag {
  id: number;
  name: string;
  slug: string;
  language_code: string;
  page_count: number;
  created_at: string;
  updated_at: string;
}

export interface Category {
  id: number;
  name: string;
  slug: string;
  description: string;
  parent_id: number | null;
  position: number;
  language_code: string;
  page_count: number;
  children?: Category[];
  created_at: string;
  updated_at: string;
}

export interface MediaVariant {
  id: number;
  type: string;
  width: number;
  height: number;
  size: number;
  url: string;
  created_at: string;
}

export interface Media {
  id: number;
  uuid: string;
  filename: string;
  mime_type: string;
  size: number;
  width: number;
  height: number;
  alt: string;
  caption: string;
  folder_id: number | null;
  uploaded_by: number;
  created_at: string;
  updated_at: string;
  urls: {
    original: string;
    thumbnail?: string;
    medium?: string;
    large?: string;
  };
  variants?: MediaVariant[];
}
