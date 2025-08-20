const int buttonPins[] = {8, 9, 10, 11,
                          2, 3, 4, 5,
                          6, 7,
                          A3, A2, A1, A0};
const int numButtons = sizeof(buttonPins) / sizeof(buttonPins[0]);
bool lastState[18]; // simpan status tombol sebelumnya

void setup() {
  Serial.begin(115200);
  for (int i = 0; i < numButtons; i++) {
    pinMode(buttonPins[i], INPUT_PULLUP); // aktifkan pull-up internal
    lastState[i] = false; // awalnya semua tidak ditekan
  }
}

void loop() {
  for (int i = 0; i < numButtons; i++) {
    bool currentState = !digitalRead(buttonPins[i]); // tombol aktif LOW

    if (currentState && !lastState[i]) {
      Serial.print(i + 1); // kirim 1–18 sesuai urutan pin
      Serial.print(";");
      lastState[i] = true;
    } else if (!currentState && lastState[i]) {
      lastState[i] = false; // reset saat dilepas
   }
 }
}