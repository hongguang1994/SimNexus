import api from './client'

export interface Contact {
  id: number
  owner_id: number
  name: string
  phone: string
  company: string
  note: string
  created_at: string
  updated_at: string
}

export type ContactInput = { name: string; phone: string; company?: string; note?: string }

export const getContactsApi = () => api.get<Contact[]>('/contacts/')
export const createContactApi = (data: ContactInput) => api.post<Contact>('/contacts/', data)
export const updateContactApi = (id: number, data: ContactInput) => api.patch<Contact>(`/contacts/${id}`, data)
export const deleteContactApi = (id: number) => api.delete(`/contacts/${id}`)
