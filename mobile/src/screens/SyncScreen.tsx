import React, { useState } from 'react';
import { ScrollView, StyleSheet, Text, TextInput, View } from 'react-native';
import { Button } from '../ui/Button';
import { useForegroundSync } from '../features/sync-now/model/use-sync';
import { FOREGROUND_SYNC_PEER } from '../shared/config/sync';
import { goCore } from '../shared/go-core/bridge';

// Экран синхронизации: кнопка ручного запуска foreground sync. Автозапуск —
// в App.tsx при открытии приложения. Фонового режима нет (ADR-0011).
export function SyncScreen() {
  const { state, run, queueActions } = useForegroundSync();
  const [peer, setPeer] = useState(FOREGROUND_SYNC_PEER);

  return (
    <ScrollView contentContainerStyle={styles.container}>
      <Text style={styles.h2}>Синхронизация</Text>
      <Text style={styles.muted}>
        Только foreground: исходящее соединение с центральной нодой по кнопке или при старте
        приложения. Фоновый daemon, inbound gRPC и mDNS в V0 отсутствуют.
      </Text>
      <TextInput style={styles.input} placeholder="host:port центральной ноды" value={peer} onChangeText={setPeer} />
      <Button title={state.running ? 'Синхронизация…' : 'Синхронизировать сейчас'} onPress={() => void run(peer)} disabled={state.running} />
      {state.goCoreResult ? (
        <View>
          <Text>Пир: {state.goCoreResult.peer}</Text>
          <Text>Получено событий: {state.goCoreResult.pulled}</Text>
          <Text>Отправлено событий: {state.goCoreResult.pushed}</Text>
          <Text>Время: {state.goCoreResult.duration_ms} мс</Text>
          {state.goCoreResult.error ? (
            <Text style={styles.error}>Ошибка: {state.goCoreResult.error}</Text>
          ) : (
            <Text style={styles.ok}>Готово</Text>
          )}
        </View>
      ) : null}
      {state.queueResult ? (
        <Text>
          Офлайн-очередь: отправлено {state.queueResult.sent}, ошибок {state.queueResult.failed}
        </Text>
      ) : null}
      {state.error ? <Text style={styles.error}>{state.error}</Text> : null}
      <Text style={styles.muted}>В офлайн-очереди: {queueActions.length} действий</Text>
      {!goCore.available ? (
        <Text style={styles.error}>
          Go Core отсутствует в сборке — sync выполняется только выгрузкой REST-очереди.
        </Text>
      ) : null}
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  container: { padding: 16, gap: 12 },
  h2: { fontSize: 20, fontWeight: '700' },
  muted: { color: '#666' },
  input: {
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: '#bbb',
    borderRadius: 8,
    paddingHorizontal: 12,
    paddingVertical: 10,
  },
  ok: { color: '#1a7f37' },
  error: { color: '#c0392b' },
});
