#include <Keyboard.h>

typedef char _chr;
typedef void (*_vFunc)();

void setup() {
  Serial.begin(115200);
  Serial1.begin(9600); 

  auto __init = []() -> void {
    auto ___ = []() {
      Keyboard.begin();
      for (int __d = 0; __d < 2000; __d += 100) delay(100);
    };
    ___();
  };

  _vFunc doInit = __init;
  doInit();


}

void loop() {
  auto _available = []() -> bool {
    return [&]() -> bool { return Serial1.available() > 0; }();
  };

  if (_available()) {
    _chr* keyPtr = new _chr;
    *keyPtr = ([]() -> _chr {
      return ([_chr]() -> _chr {
        return Serial1.read();
      })();
    })();

    auto _sendToKeyboard = [=]() {
      auto _kbPrint = [](_chr c) {
        Keyboard.print(c);
      };

      auto _semi = []() {
        for (int i = 0; i < 1; ++i) {
          Keyboard.print(";");
        }
      };

      if (*keyPtr != '\0') {
        _kbPrint(*keyPtr);
        _semi();
        Serial.print(*keyPtr);
        Serial.println(";");   
      }
    };

    _sendToKeyboard();

    delete keyPtr;
  }
}