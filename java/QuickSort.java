package com.example.algorithm;

import java.util.Arrays;

/**
 * 快速排序（Quick Sort）
 *
 * 思路：分治 + 原地划分（Lomuto 方案）
 * 1. 选择基准元素 pivot（这里取区间最后一个元素）
 * 2. 将小于等于 pivot 的元素移到左边，大于的移到右边（partition）
 * 3. 对左右两个子区间递归排序
 *
 * 时间复杂度：平均 O(n log n)，最坏 O(n^2)（已排序 + 固定 pivot）
 * 空间复杂度：O(log n)（递归栈）
 * 稳定性：不稳定
 */
public class QuickSort {

    /**
     * 对外暴露的排序入口
     *
     * @param arr 待排序数组，原地排序
     */
    public static void sort(int[] arr) {
        if (arr == null || arr.length < 2) {
            return;
        }
        quickSort(arr, 0, arr.length - 1);
    }

    /**
     * 递归主体
     */
    private static void quickSort(int[] arr, int low, int high) {
        if (low >= high) {
            return;
        }
        // 划分后 pivot 的最终位置
        int pivotIndex = partition(arr, low, high);
        quickSort(arr, low, pivotIndex - 1);
        quickSort(arr, pivotIndex + 1, high);
    }

    /**
     * Lomuto 划分方案：以 arr[high] 作为基准
     *
     * @return 基准元素最终所在的下标
     */
    private static int partition(int[] arr, int low, int high) {
        // 优化：随机选取基准，避免近乎有序数据退化成 O(n^2)
        int randomIndex = low + (int) (Math.random() * (high - low + 1));
        swap(arr, randomIndex, high);

        int pivot = arr[high];
        int i = low; // i 指向「小于等于 pivot」区域的右边界
        for (int j = low; j < high; j++) {
            if (arr[j] <= pivot) {
                swap(arr, i, j);
                i++;
            }
        }
        // 将基准放到正确位置
        swap(arr, i, high);
        return i;
    }

    /**
     * 交换数组中两个元素
     */
    private static void swap(int[] arr, int i, int j) {
        if (i == j) {
            return;
        }
        int tmp = arr[i];
        arr[i] = arr[j];
        arr[j] = tmp;
    }

    /**
     * 简单测试
     */
    public static void main(String[] args) {
        int[] arr = {5, 2, 9, 1, 5, 6, 3, 8, 7, 0};
        System.out.println("排序前: " + Arrays.toString(arr));
        QuickSort.sort(arr);
        System.out.println("排序后: " + Arrays.toString(arr));
    }
}
