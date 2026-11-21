import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { command, getBook } from './api';
import { useDemo } from './mode';
import {
  analyze,
  cancelComparison,
  decide,
  getHorseHistory,
  getNodeBook,
  saveManual,
} from './node';
import type { AnalysisInput, Decision, ManualInput, Notebook, Review } from './types';

const key = (demo: boolean) => ['field-journal', demo ? 'demo' : 'node'];
const reviewKey = ['working-comparison'];
export function useNotebook() {
  const demo = useDemo();
  const query = useQuery({
    queryKey: key(demo),
    queryFn: ({ signal }) => (demo ? getBook() : getNodeBook(signal)),
    staleTime: demo ? Infinity : 0,
  });
  const review = useQuery<Review | null>({
    queryKey: reviewKey,
    queryFn: () => null,
    initialData: null,
    enabled: false,
    gcTime: Infinity,
  });
  return {
    ...query,
    data: !demo && query.data ? { ...query.data, review: review.data } : query.data,
  };
}
export function useHorseHistory(book: Notebook, horseId?: string) {
  const demo = useDemo();
  return useQuery({
    queryKey: ['horse-history', 'node', horseId],
    enabled: !demo && Boolean(horseId),
    queryFn: ({ signal }) => getHorseHistory(horseId!, book, signal),
    staleTime: 30000,
  });
}
export function useCommand<K extends Parameters<typeof command>[0]>(route: K) {
  const cache = useQueryClient(),
    demo = useDemo();
  return useMutation({
    mutationFn: async (input: Parameters<typeof command<K>>[1]) => {
      if (demo) {
        const book = await command(route, input);
        cache.setQueryData(key(true), book);
        return;
      }
      switch (route) {
        case '/analysis': {
          const request = input as AnalysisInput;
          if (!('upload' in request)) throw new Error('Выберите снимок.');
          const review = await analyze(request.upload);
          if (review.detections.length) cache.setQueryData(reviewKey, review);
          return review;
        }
        case '/analysis/cancel': {
          await cancelComparison((input as { reviewId: string }).reviewId);
          cache.setQueryData(reviewKey, null);
          return;
        }
        case '/decisions': {
          const decision = input as Decision;
          const result = await decide(decision);
          cache.setQueryData<Review | null>(reviewKey, (review) => {
            if (!review || review.id !== decision.reviewId) return review;
            const detections = review.detections.filter((d) =>
              result.remaining_detection_ids.includes(d.id),
            );
            return detections.length ? { ...review, detections } : null;
          });
          break;
        }
        case '/manual':
          await saveManual(input as ManualInput);
          break;
        default:
          throw new Error('Действие доступно в деморежиме.');
      }
      await Promise.all([
        cache.invalidateQueries({ queryKey: key(false) }),
        cache.invalidateQueries({ queryKey: ['horse-history', 'node'] }),
      ]);
    },
    onError: () => {
      if (demo) void cache.invalidateQueries({ queryKey: key(true) });
    },
  });
}
