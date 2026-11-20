import React, { useState } from 'react';
import { ScrollView, StyleSheet, Text, TextInput, View } from 'react-native';
import { Button } from '../ui/Button';
import {
  pickPhoto,
  saveObservation,
  type PickedPhoto,
  type SaveResult,
} from '../features/new-observation/model/use-new-observation';

// Экран создания наблюдения: фото + обязательный observer_id + опциональные поля.
export function NewObservationScreen() {
  const [photo, setPhoto] = useState<PickedPhoto | null>(null);
  const [observerId, setObserverId] = useState('');
  const [horseId, setHorseId] = useState('');
  const [location, setLocation] = useState('');
  const [result, setResult] = useState<SaveResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const save = async () => {
    setBusy(true);
    setError(null);
    setResult(null);
    try {
      const res = await saveObservation(photo, observerId, horseId.trim() || null, location.trim());
      setResult(res);
      setPhoto(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <ScrollView contentContainerStyle={styles.container}>
      <Text style={styles.h2}>Новое наблюдение</Text>
      <Text style={styles.muted}>observer_id обязателен (V0 без авторизации — введите свой идентификатор)</Text>
      <TextInput style={styles.input} placeholder="observer_id" value={observerId} onChangeText={setObserverId} />
      <TextInput style={styles.input} placeholder="horse_id (необязательно)" value={horseId} onChangeText={setHorseId} />
      <TextInput style={styles.input} placeholder="Локация (необязательно)" value={location} onChangeText={setLocation} />
      <View style={styles.row}>
        <Button
          title={photo ? 'Фото выбрано — заменить' : 'Выбрать фото'}
          onPress={() => {
            pickPhoto()
              .then((p) => setPhoto(p))
              .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)));
          }}
        />
      </View>
      <Button title={busy ? 'Сохранение…' : 'Сохранить наблюдение'} onPress={() => void save()} disabled={busy} />
      {result ? <Text style={styles.ok}>[{result.where}] {result.message}</Text> : null}
      {error ? <Text style={styles.error}>{error}</Text> : null}
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
  row: { gap: 8 },
  ok: { color: '#1a7f37' },
  error: { color: '#c0392b' },
});
