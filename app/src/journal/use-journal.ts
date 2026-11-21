import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { command, getBook } from './api';
const key = ['field-journal'];
export function useNotebook() {
  return useQuery({ queryKey: key, queryFn: getBook, staleTime: Infinity });
}
export function useCommand<K extends Parameters<typeof command>[0]>(route: K) {
  const cache = useQueryClient();
  return useMutation({
    mutationFn: (input: Parameters<typeof command<K>>[1]) => command(route, input),
    onSuccess: (book) => cache.setQueryData(key, book),
    onError: () => cache.invalidateQueries({ queryKey: key }),
  });
}
