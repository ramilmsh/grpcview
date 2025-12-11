<script setup lang="ts">
import { ref } from "vue";

const props = defineProps<{
  show: boolean;
}>();

const emit = defineEmits<{
  (e: "close"): void;
  (e: "add-reflection", host: string, port: number): void;
  (e: "add-descriptor", file: File): void;
}>();

const mode = ref<"reflection" | "file">("reflection");
const host = ref("127.0.0.1");
const port = ref(10000);
const file = ref<File | null>(null);

const onFileChange = (event: Event) => {
  const target = event.target as HTMLInputElement;
  if (target.files && target.files.length > 0) {
    file.value = target.files[0];
  }
};

const submit = () => {
  if (mode.value === "reflection") {
    emit("add-reflection", host.value, port.value);
  } else {
    if (file.value) {
      emit("add-descriptor", file.value);
    }
  }
  close();
};

const close = () => {
  emit("close");
};
</script>

<template>
  <div v-if="show" class="modal-overlay" @click.self="close">
    <div class="modal">
      <div class="modal-header">
        <h3>Add Source</h3>
        <button class="close-btn" @click="close">×</button>
      </div>
      <div class="modal-body">
        <div class="tabs">
          <button
            :class="{ active: mode === 'reflection' }"
            @click="mode = 'reflection'"
          >
            Reflection
          </button>
          <button :class="{ active: mode === 'file' }" @click="mode = 'file'">
            Descriptor Set
          </button>
        </div>

        <div v-if="mode === 'reflection'" class="form-group">
          <label>Host</label>
          <input v-model="host" type="text" placeholder="localhost" />
          <label>Port</label>
          <input v-model="port" type="number" placeholder="8080" />
        </div>

        <div v-if="mode === 'file'" class="form-group">
          <label>File Descriptor Set (.pb, .bin, .desc)</label>
          <input type="file" @change="onFileChange" accept=".pb,.bin,.desc" />
        </div>
      </div>
      <div class="modal-footer">
        <button class="cancel-btn" @click="close">Cancel</button>
        <button class="add-btn" @click="submit">Add</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.modal-overlay {
  position: fixed;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  justify-content: center;
  align-items: center;
  z-index: 1000;
}

.modal {
  background: white;
  border-radius: 8px;
  width: 400px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
  display: flex;
  flex-direction: column;
}

.modal-header {
  padding: 16px;
  border-bottom: 1px solid #eee;
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.modal-header h3 {
  margin: 0;
  font-size: 18px;
}

.close-btn {
  background: none;
  border: none;
  font-size: 24px;
  cursor: pointer;
  padding: 0;
  line-height: 1;
}

.modal-body {
  padding: 16px;
}

.tabs {
  display: flex;
  margin-bottom: 16px;
  border-bottom: 1px solid #eee;
}

.tabs button {
  padding: 8px 16px;
  border: none;
  background: none;
  cursor: pointer;
  font-weight: 500;
  color: #666;
  border-bottom: 2px solid transparent;
}

.tabs button.active {
  color: #6200ee;
  border-bottom-color: #6200ee;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

input {
  padding: 8px;
  border: 1px solid #ddd;
  border-radius: 4px;
}

.modal-footer {
  padding: 16px;
  border-top: 1px solid #eee;
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

button.add-btn {
  background: #6200ee;
  color: white;
  padding: 8px 16px;
  border-radius: 4px;
  border: none;
  cursor: pointer;
}

button.cancel-btn {
  background: white;
  color: #666;
  padding: 8px 16px;
  border-radius: 4px;
  border: 1px solid #ddd;
  cursor: pointer;
}
</style>
